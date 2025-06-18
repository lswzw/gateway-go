package circuitbreaker

import (
	"fmt"
	"gateway-go/internal/plugin/core"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

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

// CircuitBreakerPlugin 熔断器插件
type CircuitBreakerPlugin struct {
	*core.BasePlugin
	config          map[string]interface{}
	circuitBreakers map[string]*CircuitBreaker
	mu              sync.RWMutex
	stopCh          chan struct{} // 停止信号通道
}

// New 创建熔断器插件
func New() *CircuitBreakerPlugin {
	return &CircuitBreakerPlugin{
		BasePlugin:      core.NewBasePlugin("circuit_breaker", 5, nil),
		circuitBreakers: make(map[string]*CircuitBreaker),
		stopCh:          make(chan struct{}),
	}
}

// Init 初始化插件
func (p *CircuitBreakerPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap

	// 启动自动清理
	go p.startCleanup()

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
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "服务暂时不可用",
		})
		ctx.Abort()
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

// startCleanup 启动自动清理
func (p *CircuitBreakerPlugin) startCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.cleanupInactiveCircuitBreakers()
		case <-p.stopCh:
			return
		}
	}
}

// cleanupInactiveCircuitBreakers 清理不活跃的熔断器
func (p *CircuitBreakerPlugin) cleanupInactiveCircuitBreakers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for target, cb := range p.circuitBreakers {
		// 如果熔断器超过30分钟未使用，则清理
		if now.Sub(cb.lastUsed) > 30*time.Minute {
			delete(p.circuitBreakers, target)
		}
	}
}

// getCircuitBreaker 获取或创建熔断器
func (p *CircuitBreakerPlugin) getCircuitBreaker(target string) *CircuitBreaker {
	p.mu.RLock()
	cb, exists := p.circuitBreakers[target]
	p.mu.RUnlock()

	if exists {
		// 更新最后使用时间
		cb.lastUsed = time.Now()
		return cb
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	cb, exists = p.circuitBreakers[target]
	if exists {
		cb.lastUsed = time.Now()
		return cb
	}

	// 从配置中获取参数
	failureThreshold := 5
	recoveryTimeout := 30
	halfOpenQuota := 2
	successThreshold := 3
	windowSize := 10

	if ft, ok := p.config["failure_threshold"].(int); ok {
		failureThreshold = ft
	}
	if rt, ok := p.config["recovery_timeout"].(int); ok {
		recoveryTimeout = rt
	}
	if hoq, ok := p.config["half_open_quota"].(int); ok {
		halfOpenQuota = hoq
	}
	if st, ok := p.config["success_threshold"].(int); ok {
		successThreshold = st
	}
	if ws, ok := p.config["window_size"].(int); ok {
		windowSize = ws
	}

	// 创建新的熔断器
	cb = &CircuitBreaker{
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
	p.circuitBreakers[target] = cb

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

// Stop 停止插件
func (p *CircuitBreakerPlugin) Stop() error {
	close(p.stopCh)
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
