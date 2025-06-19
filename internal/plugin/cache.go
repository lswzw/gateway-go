package plugin

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// PluginResult 插件执行结果
type PluginResult struct {
	Success bool
	Error   error
	Data    map[string]interface{}
	Expire  time.Time
}

// PluginCache 插件结果缓存
type PluginCache struct {
	cache map[string]*PluginResult
	mu    sync.RWMutex
	ttl   time.Duration
}

// NewPluginCache 创建插件缓存
func NewPluginCache(ttl time.Duration) *PluginCache {
	pc := &PluginCache{
		cache: make(map[string]*PluginResult),
		ttl:   ttl,
	}
	// 启动清理过期缓存的goroutine
	go pc.cleanup()
	return pc
}

// Get 获取缓存结果（兼容 chain.PluginCacheIface）
func (pc *PluginCache) Get(key string) (interface{}, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	result, exists := pc.cache[key]
	if !exists {
		return nil, false
	}
	if time.Now().After(result.Expire) {
		return nil, false
	}
	return result.Data, true
}

// Set 设置缓存结果（兼容 chain.PluginCacheIface）
func (pc *PluginCache) Set(key string, data interface{}) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.cache[key] = &PluginResult{
		Success: true,
		Error:   nil,
		Data:    data.(map[string]interface{}),
		Expire:  time.Now().Add(pc.ttl),
	}
}

// cleanup 清理过期缓存
func (pc *PluginCache) cleanup() {
	ticker := time.NewTicker(pc.ttl)
	defer ticker.Stop()
	for range ticker.C {
		pc.mu.Lock()
		now := time.Now()
		for key, result := range pc.cache {
			if now.After(result.Expire) {
				delete(pc.cache, key)
			}
		}
		pc.mu.Unlock()
	}
}

// GenerateCacheKey 生成缓存键
func GenerateCacheKey(ctx *gin.Context, pluginName string) string {
	data := map[string]interface{}{
		"plugin": pluginName,
		"path":   ctx.Request.URL.Path,
		"method": ctx.Request.Method,
		"host":   ctx.Request.Host,
		"query":  ctx.Request.URL.RawQuery,
	}
	// 添加关键请求头
	for _, header := range []string{"Authorization", "Content-Type", "User-Agent"} {
		if value := ctx.GetHeader(header); value != "" {
			data[header] = value
		}
	}
	jsonData, _ := json.Marshal(data)
	hash := md5.Sum(jsonData)
	return hex.EncodeToString(hash[:])
}
