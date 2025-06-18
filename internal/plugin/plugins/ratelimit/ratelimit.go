package ratelimit

import (
	"fmt"
	"gateway-go/internal/plugin/core"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// TokenBucket 令牌桶
type TokenBucket struct {
	rate       float64 // 令牌生成速率
	capacity   int64   // 桶容量
	tokens     int64   // 当前令牌数
	lastRefill int64   // 上次填充时间
	mu         sync.RWMutex
}

// RateLimitPlugin 限流插件
type RateLimitPlugin struct {
	*core.BasePlugin
	config   map[string]interface{}
	buckets  map[string]*TokenBucket
	mu       sync.RWMutex
	cleanup  *time.Ticker
	stopChan chan struct{}
}

// New 创建限流插件
func New() *RateLimitPlugin {
	return &RateLimitPlugin{
		BasePlugin: core.NewBasePlugin("rate_limit", 10, nil),
		buckets:    make(map[string]*TokenBucket),
		cleanup:    time.NewTicker(5 * time.Minute),
		stopChan:   make(chan struct{}),
	}
}

// Init 初始化插件
func (p *RateLimitPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap

	// 启动清理协程
	go p.cleanupLoop()

	return nil
}

// Execute 执行插件
func (p *RateLimitPlugin) Execute(ctx *gin.Context) error {
	// 获取限流键
	key := p.getLimitKey(ctx)

	// 获取或创建令牌桶
	bucket := p.getBucket(key)

	// 尝试获取令牌
	if !bucket.allow() {
		ctx.JSON(http.StatusTooManyRequests, gin.H{
			"error": "请求过于频繁",
		})
		ctx.Abort()
		return nil
	}

	return nil
}

// getLimitKey 获取限流键
func (p *RateLimitPlugin) getLimitKey(c *gin.Context) string {
	// 如果配置了基于IP限流，则使用IP作为键
	if ipBased, ok := p.config["ip_based"].(bool); ok && ipBased {
		return c.ClientIP()
	}

	// 默认使用路径作为限流键
	return c.Request.URL.Path
}

// getBucket 获取或创建令牌桶
func (p *RateLimitPlugin) getBucket(key string) *TokenBucket {
	p.mu.RLock()
	bucket, exists := p.buckets[key]
	p.mu.RUnlock()

	if exists {
		return bucket
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	bucket, exists = p.buckets[key]
	if exists {
		return bucket
	}

	// 从配置中获取参数
	requestsPerSecond := 10.0
	burst := 20

	if rps, ok := p.config["requests_per_second"].(float64); ok {
		requestsPerSecond = rps
	}
	if b, ok := p.config["burst"].(int); ok {
		burst = b
	}

	// 创建新的令牌桶
	bucket = &TokenBucket{
		rate:       requestsPerSecond,
		capacity:   int64(burst),
		tokens:     int64(burst),
		lastRefill: time.Now().UnixNano(),
	}
	p.buckets[key] = bucket

	return bucket
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

// cleanupLoop 清理过期令牌桶
func (p *RateLimitPlugin) cleanupLoop() {
	for {
		select {
		case <-p.cleanup.C:
			p.cleanupBuckets()
		case <-p.stopChan:
			p.cleanup.Stop()
			return
		}
	}
}

// cleanupBuckets 清理过期令牌桶
func (p *RateLimitPlugin) cleanupBuckets() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().UnixNano()
	for key, bucket := range p.buckets {
		bucket.mu.RLock()
		lastRefill := bucket.lastRefill
		bucket.mu.RUnlock()

		// 如果超过5分钟没有使用，则删除
		if now-lastRefill > 5*time.Minute.Nanoseconds() {
			delete(p.buckets, key)
		}
	}
}

// Stop 停止插件
func (p *RateLimitPlugin) Stop() error {
	close(p.stopChan)
	return nil
}

// min 返回两个int64中的较小值
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
