package ratelimit

import (
	"container/list"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/metrics"
	"gateway-go/internal/netutil"
	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

// DefaultMaxEntries 默认最大令牌桶容量限制 (REQ-P1-02)
const DefaultMaxEntries = 10000

// 限流默认参数（与插件 README 保持一致）
const (
	// DefaultRate 默认每秒放行请求数
	DefaultRate = 10.0
	// DefaultBurst 默认令牌桶容量
	DefaultBurst = 20
	// DefaultUserHeader 按用户维度限流时默认读取的请求头
	DefaultUserHeader = "X-User-ID"
)

// 限流维度
const (
	// DimensionPath 按 URL 路径限流（历史默认行为：每个路径一个桶）
	DimensionPath = "path"
	// DimensionIP 按客户端 IP 限流
	DimensionIP = "ip"
	// DimensionUser 按用户标识限流
	DimensionUser = "user"
	// DimensionGlobal 全局单一桶限流
	DimensionGlobal = "global"
)

// TokenBucket 令牌桶
type TokenBucket struct {
	rate       float64 // 令牌生成速率
	capacity   int64   // 桶容量
	tokens     int64   // 当前令牌数
	lastRefill int64   // 上次填充时间
	mu         sync.RWMutex
}

// bucketNode 封装 LRU 链表节点信息
type bucketNode struct {
	key    string
	bucket *TokenBucket
}

// RateLimitPlugin 限流插件 (支持 LRU 容量限制与幂等生命周期 REQ-P1-02)
type RateLimitPlugin struct {
	*core.BasePlugin
	config         map[string]interface{}
	rate           float64                  // 令牌生成速率 requests_per_second（Init 阶段归一化）
	burst          int64                    // 令牌桶容量 burst（Init 阶段归一化）
	dimension      string                   // 限流维度：ip / user / global / path
	userHeader     string                   // user 维度读取的请求头
	configured     bool                     // Init 是否已执行（未初始化时使用默认参数）
	buckets        map[string]*list.Element // key -> *list.Element with value *bucketNode
	lruList        *list.List               // Front = 最常访问 (MRU), Back = 最久未访问 (LRU)
	maxEntries     int                      // 最大令牌桶数量上限
	mu             sync.Mutex               // 保护 buckets 与 lruList
	cleanup        *time.Ticker
	stopChan       chan struct{}
	stopOnce       sync.Once
	running        bool
	trustedProxies []string
}

// New 创建限流插件
func New() *RateLimitPlugin {
	return &RateLimitPlugin{
		BasePlugin: core.NewBasePlugin("rate_limit", 10, nil),
		buckets:    make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: DefaultMaxEntries,
	}
}

// Init 初始化插件 (幂等且可重入：重复调用先清理旧后台协程，无协程泄漏 REQ-P1-02)
func (p *RateLimitPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.mu.Lock()
	p.config = configMap

	// 解析最大容量上限 max_entries (默认 10000)
	p.maxEntries = DefaultMaxEntries
	if me, ok := toInt64(configMap["max_entries"]); ok && me > 0 {
		p.maxEntries = int(me)
	}

	// 解析速率与桶容量。YAML/JSON 解码后可能是 int/int64/float64/字符串，必须归一化：
	// 历史实现只做 float64 断言，于是 config.yaml 里 requests_per_second: 100 会被
	// 静默回落成 10 rps（实测 10s 内只放行 299 次，而正确值应为约 1,196 次）。
	p.rate = DefaultRate
	if rps, ok := toFloat64(configMap["requests_per_second"]); ok && rps >= 0 {
		p.rate = rps
	}
	p.burst = DefaultBurst
	if b, ok := toInt64(configMap["burst"]); ok && b >= 0 {
		p.burst = b
	}
	p.configured = true

	// 限流维度：dimension 优先，ip_based 作为历史别名
	p.dimension = resolveDimension(configMap)
	p.userHeader = DefaultUserHeader
	if h, ok := configMap["user_header"].(string); ok && strings.TrimSpace(h) != "" {
		p.userHeader = strings.TrimSpace(h)
	}

	// 初始化可信代理
	p.trustedProxies = nil
	if tp, ok := configMap["trusted_proxies"].([]interface{}); ok {
		for _, item := range tp {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				p.trustedProxies = append(p.trustedProxies, strings.TrimSpace(s))
			}
		}
	} else if tpStr, ok := configMap["trusted_proxies"].([]string); ok {
		p.trustedProxies = append(p.trustedProxies, tpStr...)
	}

	// 停止先前的清理协程，防止重复调用 Init 造成协程泄漏
	if p.running && p.stopChan != nil {
		close(p.stopChan)
		if p.cleanup != nil {
			p.cleanup.Stop()
		}
		p.running = false
	}

	// 分配新的生命周期通道并启动清理协程
	p.stopChan = make(chan struct{})
	p.stopOnce = sync.Once{}
	p.cleanup = time.NewTicker(5 * time.Minute)
	p.running = true

	stopChan := p.stopChan
	ticker := p.cleanup
	p.mu.Unlock()

	go p.cleanupLoop(stopChan, ticker)

	return nil
}

// Execute 执行插件
func (p *RateLimitPlugin) Execute(ctx *gin.Context) error {
	// 获取限流键
	key := p.getLimitKey(ctx)

	// 获取或创建令牌桶 (受 max_entries 与 LRU 限制)
	bucket := p.getBucket(key)

	// 尝试获取令牌
	if !bucket.allow() {
		metrics.RecordRateLimitRejected(ctx.GetString("route_name"))
		response.RespondError(ctx, http.StatusTooManyRequests, "请求过于频繁", response.GetRequestID(ctx))
		return nil
	}

	return nil
}

// getLimitKey 获取限流键
//
// 支持 dimension: ip / user / global / path（默认 path，兼容历史行为）。
// 历史实现只识别 ip_based 布尔开关，导致文档化的 dimension: ip 实际退化成
// "按 URL 路径全局限流"，所有客户端共享一个令牌桶。
func (p *RateLimitPlugin) getLimitKey(c *gin.Context) string {
	switch p.dimension {
	case DimensionIP:
		return p.clientIP(c)
	case DimensionUser:
		if v := strings.TrimSpace(c.GetHeader(p.userHeader)); v != "" {
			return v
		}
		// 未携带用户标识时退化为按 IP 限流，避免退化成全局限流误伤所有用户
		return p.clientIP(c)
	case DimensionGlobal:
		return DimensionGlobal
	default:
		return c.Request.URL.Path
	}
}

// clientIP 返回可信代理链解析后的客户端 IP
func (p *RateLimitPlugin) clientIP(c *gin.Context) string {
	trusted := p.trustedProxies
	if len(trusted) == 0 {
		trusted = netutil.GetDefaultTrustedProxies()
	}
	return netutil.ClientIP(c, trusted)
}

// getBucket 获取或创建令牌桶 (LRU 淘汰保证内存有界 REQ-P1-02)
func (p *RateLimitPlugin) getBucket(key string) *TokenBucket {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 命中缓存：移动到 LRU 队列头部
	if elem, exists := p.buckets[key]; exists {
		p.lruList.MoveToFront(elem)
		return elem.Value.(*bucketNode).bucket
	}

	// 容量达到上限：淘汰最久未访问 (LRU) 的桶
	for p.lruList.Len() >= p.maxEntries && p.lruList.Len() > 0 {
		oldest := p.lruList.Back()
		if oldest != nil {
			node := oldest.Value.(*bucketNode)
			delete(p.buckets, node.key)
			p.lruList.Remove(oldest)
		}
	}

	// 桶参数已在 Init 阶段完成类型归一化；未执行 Init 时退回默认参数。
	// 注意 burst=0 是合法配置（桶容量 0，等价于全部限流），不可被默认值覆盖。
	rate, capacity := p.rate, p.burst
	if !p.configured {
		rate, capacity = DefaultRate, DefaultBurst
	}

	// 创建新的令牌桶
	bucket := &TokenBucket{
		rate:       rate,
		capacity:   capacity,
		tokens:     capacity,
		lastRefill: time.Now().UnixNano(),
	}

	node := &bucketNode{
		key:    key,
		bucket: bucket,
	}
	elem := p.lruList.PushFront(node)
	p.buckets[key] = elem

	return bucket
}

// BucketCount 返回当前桶的数量（供监控与单测验证容量上限）
func (p *RateLimitPlugin) BucketCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buckets)
}

// MaxEntries 返回当前最大容量上限
func (p *RateLimitPlugin) MaxEntries() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxEntries
}

// allow 尝试获取令牌
func (b *TokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().UnixNano()
	elapsed := float64(now-b.lastRefill) / float64(time.Second)

	// 计算需要添加的令牌数
	tokensToAdd := int64(elapsed * b.rate)
	if tokensToAdd > 0 {
		b.tokens = min(b.capacity, b.tokens+tokensToAdd)
		b.lastRefill = now
	}

	// 尝试获取令牌
	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

// cleanupLoop 定期清理长时间不活跃的令牌桶
func (p *RateLimitPlugin) cleanupLoop(stopChan <-chan struct{}, ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
			p.cleanupBuckets()
		case <-stopChan:
			return
		}
	}
}

// cleanupBuckets 清理超过 5 分钟未更新的过期令牌桶
func (p *RateLimitPlugin) cleanupBuckets() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().UnixNano()
	var next *list.Element
	for elem := p.lruList.Back(); elem != nil; elem = next {
		next = elem.Prev()
		node := elem.Value.(*bucketNode)
		node.bucket.mu.RLock()
		lastRefill := node.bucket.lastRefill
		node.bucket.mu.RUnlock()

		// 如果超过5分钟没有使用，则删除
		if now-lastRefill > 5*time.Minute.Nanoseconds() {
			delete(p.buckets, node.key)
			p.lruList.Remove(elem)
		}
	}
}

// Stop 停止插件 (sync.Once 保护，重复调用安全不 panic REQ-P1-02)
func (p *RateLimitPlugin) Stop() error {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.running {
			p.running = false
			if p.cleanup != nil {
				p.cleanup.Stop()
			}
			if p.stopChan != nil {
				close(p.stopChan)
			}
		}
	})
	return nil
}

// min 返回两个int64中的较小值
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// resolveDimension 解析限流维度
//
// 优先级：dimension 字段 > ip_based（历史别名）> path（历史默认行为）。
// 非法/未配置时回落到 path，保持与既有部署的兼容性。
func resolveDimension(cfg map[string]interface{}) string {
	if raw, ok := cfg["dimension"].(string); ok {
		switch dim := strings.ToLower(strings.TrimSpace(raw)); dim {
		case DimensionIP, DimensionUser, DimensionGlobal:
			return dim
		}
	}
	if ipBased, ok := cfg["ip_based"].(bool); ok && ipBased {
		return DimensionIP
	}
	return DimensionPath
}

// toFloat64 将配置值归一化为 float64
//
// 配置文件经 YAML/JSON 解码后数值可能是 int / int64 / uint64 / float32 / float64 /
// 字符串，单一类型断言会造成静默回落，因此统一走这里转换。
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// toInt64 将配置值归一化为 int64
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}
