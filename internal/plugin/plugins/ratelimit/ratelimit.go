package ratelimit

import (
	"container/list"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/netutil"
	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

// DefaultMaxEntries 默认最大令牌桶容量限制 (REQ-P1-02)
const DefaultMaxEntries = 10000

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
	if me, ok := configMap["max_entries"].(int); ok && me > 0 {
		p.maxEntries = me
	} else if me64, ok := configMap["max_entries"].(int64); ok && me64 > 0 {
		p.maxEntries = int(me64)
	} else if meF, ok := configMap["max_entries"].(float64); ok && meF > 0 {
		p.maxEntries = int(meF)
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
		response.RespondError(ctx, http.StatusTooManyRequests, "请求过于频繁", response.GetRequestID(ctx))
		return nil
	}

	return nil
}

// getLimitKey 获取限流键
func (p *RateLimitPlugin) getLimitKey(c *gin.Context) string {
	// 如果配置了基于IP限流，则使用IP作为键
	if ipBased, ok := p.config["ip_based"].(bool); ok && ipBased {
		trusted := p.trustedProxies
		if len(trusted) == 0 {
			trusted = netutil.GetDefaultTrustedProxies()
		}
		return netutil.ClientIP(c, trusted)
	}

	// 默认使用路径作为限流键
	return c.Request.URL.Path
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

	// 从配置中获取参数
	requestsPerSecond := 10.0
	burst := 20

	if rps, ok := p.config["requests_per_second"].(float64); ok {
		requestsPerSecond = rps
	}
	if b, ok := p.config["burst"].(int); ok {
		burst = b
	} else if bF, ok := p.config["burst"].(float64); ok {
		burst = int(bF)
	}

	// 创建新的令牌桶
	bucket := &TokenBucket{
		rate:       requestsPerSecond,
		capacity:   int64(burst),
		tokens:     int64(burst),
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
