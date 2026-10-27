package circuitbreaker

import (
	"container/list"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

// DefaultMaxEntries 默认最大熔断器实例上限 (REQ-P1-02)
const DefaultMaxEntries = 1000

// CircuitBreakerState 熔断器状态
type CircuitBreakerState int32

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	state         int32 // CircuitBreakerState
	halfOpenQuota int32
	window        *Window
	config        map[string]interface{}
	lastUsed      time.Time // 最后使用时间
}

// breakerNode 封装 LRU 链表节点信息
type breakerNode struct {
	target  string
	breaker *CircuitBreaker
}

// CircuitBreakerPlugin 熔断器插件 (支持容量上限 LRU 淘汰与幂等生命周期 REQ-P1-02)
type CircuitBreakerPlugin struct {
	*core.BasePlugin
	config          map[string]interface{}
	circuitBreakers map[string]*list.Element // target -> *list.Element with value *breakerNode
	lruList         *list.List               // Front = MRU, Back = LRU
	maxEntries      int                      // 最大熔断器实例上限
	mu              sync.Mutex               // 保护 circuitBreakers 与 lruList
	cleanup         *time.Ticker
	stopCh          chan struct{} // 停止信号通道
	stopOnce        sync.Once
	running         bool
}

// New 创建熔断器插件
func New() *CircuitBreakerPlugin {
	return &CircuitBreakerPlugin{
		BasePlugin:      core.NewBasePlugin("circuit_breaker", 5, nil),
		circuitBreakers: make(map[string]*list.Element),
		lruList:         list.New(),
		maxEntries:      DefaultMaxEntries,
	}
}

// Init 初始化插件 (幂等且可重入：重复调用先清理旧后台协程，无协程泄漏 REQ-P1-02)
func (p *CircuitBreakerPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.mu.Lock()
	p.config = configMap

	// 解析最大容量限制 max_entries
	p.maxEntries = DefaultMaxEntries
	if me, ok := configMap["max_entries"].(int); ok && me > 0 {
		p.maxEntries = me
	} else if me64, ok := configMap["max_entries"].(int64); ok && me64 > 0 {
		p.maxEntries = int(me64)
	} else if meF, ok := configMap["max_entries"].(float64); ok && meF > 0 {
		p.maxEntries = int(meF)
	}

	// 若已有清理协程在运行，先停止旧协程以防协程泄漏
	if p.running && p.stopCh != nil {
		close(p.stopCh)
		if p.cleanup != nil {
			p.cleanup.Stop()
		}
		p.running = false
	}

	// 启动新的自动清理协程
	p.stopCh = make(chan struct{})
	p.stopOnce = sync.Once{}
	p.cleanup = time.NewTicker(5 * time.Minute)
	p.running = true

	stopCh := p.stopCh
	ticker := p.cleanup
	p.mu.Unlock()

	go p.cleanupLoop(stopCh, ticker)

	return nil
}

// Execute 执行插件
func (p *CircuitBreakerPlugin) Execute(ctx *gin.Context) error {
	// 获取目标服务
	target := ctx.GetString("target")
	if target == "" {
		// 如果没有目标信息，使用请求路径作为标识
		target = ctx.Request.URL.Path
	}

	// 获取或创建熔断器
	cb := p.getCircuitBreaker(target)

	// 检查熔断器状态
	if !cb.allowRequest() {
		response.RespondError(ctx, http.StatusServiceUnavailable, "服务暂时不可用", response.GetRequestID(ctx))
		return nil
	}

	// 设置响应写入器以捕获状态码
	writer := &responseWriter{
		ResponseWriter: ctx.Writer,
		circuitBreaker: cb,
	}
	ctx.Writer = writer

	return nil
}

// cleanupLoop 自动定期清理不活跃熔断器
func (p *CircuitBreakerPlugin) cleanupLoop(stopCh <-chan struct{}, ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
			p.cleanupInactiveCircuitBreakers()
		case <-stopCh:
			return
		}
	}
}

// cleanupInactiveCircuitBreakers 清理超过 30 分钟不活跃的熔断器
func (p *CircuitBreakerPlugin) cleanupInactiveCircuitBreakers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var next *list.Element
	for elem := p.lruList.Back(); elem != nil; elem = next {
		next = elem.Prev()
		node := elem.Value.(*breakerNode)
		// 如果熔断器超过30分钟未使用，则清理
		if now.Sub(node.breaker.lastUsed) > 30*time.Minute {
			delete(p.circuitBreakers, node.target)
			p.lruList.Remove(elem)
		}
	}
}

// GetCircuitBreaker 获取或创建熔断器（导出供管理与测试使用）
func (p *CircuitBreakerPlugin) GetCircuitBreaker(target string) *CircuitBreaker {
	return p.getCircuitBreaker(target)
}

// BreakerCount 返回当前熔断器实例数量
func (p *CircuitBreakerPlugin) BreakerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.circuitBreakers)
}

// MaxEntries 返回当前最大熔断器实例上限
func (p *CircuitBreakerPlugin) MaxEntries() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxEntries
}

// SetState 设置熔断器状态
func (cb *CircuitBreaker) SetState(state CircuitBreakerState) {
	atomic.StoreInt32(&cb.state, int32(state))
}

// getCircuitBreaker 获取或创建熔断器 (LRU 淘汰保证内存有界 REQ-P1-02)
func (p *CircuitBreakerPlugin) getCircuitBreaker(target string) *CircuitBreaker {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 命中缓存：移动到 LRU 队列头部
	if elem, exists := p.circuitBreakers[target]; exists {
		p.lruList.MoveToFront(elem)
		node := elem.Value.(*breakerNode)
		node.breaker.lastUsed = time.Now()
		return node.breaker
	}

	// 容量达到上限：淘汰最久未访问 (LRU) 的熔断器实例
	for p.lruList.Len() >= p.maxEntries && p.lruList.Len() > 0 {
		oldest := p.lruList.Back()
		if oldest != nil {
			node := oldest.Value.(*breakerNode)
			delete(p.circuitBreakers, node.target)
			p.lruList.Remove(oldest)
		}
	}

	// 从配置中获取参数
	failureThreshold := 5
	recoveryTimeout := 30
	halfOpenQuota := 2
	successThreshold := 3
	windowSize := 10

	if ft, ok := p.config["failure_threshold"].(int); ok {
		failureThreshold = ft
	} else if ftF, ok := p.config["failure_threshold"].(float64); ok {
		failureThreshold = int(ftF)
	}
	if rt, ok := p.config["recovery_timeout"].(int); ok {
		recoveryTimeout = rt
	} else if rtF, ok := p.config["recovery_timeout"].(float64); ok {
		recoveryTimeout = int(rtF)
	}
	if hoq, ok := p.config["half_open_quota"].(int); ok {
		halfOpenQuota = hoq
	} else if hoqF, ok := p.config["half_open_quota"].(float64); ok {
		halfOpenQuota = int(hoqF)
	}
	if st, ok := p.config["success_threshold"].(int); ok {
		successThreshold = st
	} else if stF, ok := p.config["success_threshold"].(float64); ok {
		successThreshold = int(stF)
	}
	if ws, ok := p.config["window_size"].(int); ok {
		windowSize = ws
	} else if wsF, ok := p.config["window_size"].(float64); ok {
		windowSize = int(wsF)
	}

	// 创建新的熔断器
	cb := &CircuitBreaker{
		state:         int32(StateClosed),
		halfOpenQuota: int32(halfOpenQuota),
		config: map[string]interface{}{
			"failure_threshold": failureThreshold,
			"recovery_timeout":  recoveryTimeout,
			"half_open_quota":   halfOpenQuota,
			"success_threshold": successThreshold,
			"window_size":       windowSize,
		},
		window:   NewWindow(10, time.Duration(windowSize)*time.Second),
		lastUsed: time.Now(),
	}

	node := &breakerNode{
		target:  target,
		breaker: cb,
	}
	elem := p.lruList.PushFront(node)
	p.circuitBreakers[target] = elem

	return cb
}

// allowRequest 检查是否允许请求
func (cb *CircuitBreaker) allowRequest() bool {
	state := atomic.LoadInt32(&cb.state)

	switch CircuitBreakerState(state) {
	case StateClosed:
		// 检查失败率是否超过阈值
		failureThreshold := cb.config["failure_threshold"].(int)
		if cb.window.GetFailureRate() >= float64(failureThreshold)/100.0 {
			atomic.StoreInt32(&cb.state, int32(StateOpen))
			return false
		}
		return true
	case StateOpen:
		// 检查是否达到恢复时间
		recoveryTimeout := cb.config["recovery_timeout"].(int)
		if time.Since(time.Unix(0, cb.window.getCurrentBucket().timestamp)) >= time.Duration(recoveryTimeout)*time.Second {
			// 尝试转换为半开状态
			if atomic.CompareAndSwapInt32(&cb.state, int32(StateOpen), int32(StateHalfOpen)) {
				halfOpenQuota := cb.config["half_open_quota"].(int)
				atomic.StoreInt32(&cb.halfOpenQuota, int32(halfOpenQuota))
				return true
			}
		}
		return false
	case StateHalfOpen:
		// 检查半开配额
		quota := atomic.AddInt32(&cb.halfOpenQuota, -1)
		return quota >= 0
	default:
		return true
	}
}

// recordFailure 记录失败
func (cb *CircuitBreaker) recordFailure() {
	cb.window.RecordFailure()
}

// recordSuccess 记录成功
func (cb *CircuitBreaker) recordSuccess() {
	cb.window.RecordSuccess()
	state := atomic.LoadInt32(&cb.state)
	if CircuitBreakerState(state) == StateHalfOpen {
		// 检查成功率是否达到阈值
		successThreshold := cb.config["success_threshold"].(int)
		if cb.window.GetFailureRate() < float64(successThreshold)/100.0 {
			// 重置熔断器状态
			atomic.StoreInt32(&cb.state, int32(StateClosed))
		}
	}
}

// Stop 停止插件 (sync.Once 保护，重复调用安全不 panic REQ-P1-02)
func (p *CircuitBreakerPlugin) Stop() error {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.running {
			p.running = false
			if p.cleanup != nil {
				p.cleanup.Stop()
			}
			if p.stopCh != nil {
				close(p.stopCh)
			}
		}
	})
	return nil
}

// responseWriter 响应写入器，用于捕获状态码
type responseWriter struct {
	gin.ResponseWriter
	circuitBreaker *CircuitBreaker
	statusWritten  bool
}

// WriteHeader 写入状态码
func (w *responseWriter) WriteHeader(code int) {
	if !w.statusWritten {
		w.statusWritten = true
		// 根据状态码更新熔断器
		if code >= 500 {
			w.circuitBreaker.recordFailure()
		} else {
			w.circuitBreaker.recordSuccess()
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write 写入响应体
func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.statusWritten {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
