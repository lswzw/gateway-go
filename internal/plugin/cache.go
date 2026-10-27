package plugin

import (
	"container/list"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// DefaultCacheMaxEntries 默认插件结果缓存容量限制 (REQ-P1-02)
const DefaultCacheMaxEntries = 10000

// PluginResult 插件执行结果
type PluginResult struct {
	Success bool
	Error   error
	Data    map[string]interface{}
	Expire  time.Time
}

// cacheNode 封装 LRU 链表节点信息
type cacheNode struct {
	key    string
	result *PluginResult
}

// PluginCache 插件结果缓存 (支持 LRU 容量限制与幂等退出 REQ-P1-02)
type PluginCache struct {
	cache      map[string]*list.Element // key -> *list.Element with value *cacheNode
	lruList    *list.List               // Front = MRU, Back = LRU
	maxEntries int                      // 最大缓存容量
	mu         sync.Mutex               // 保护 cache 与 lruList
	ttl        time.Duration
	stopChan   chan struct{}
	stopOnce   sync.Once
}

// NewPluginCache 创建插件缓存 (默认容量 10000)
func NewPluginCache(ttl time.Duration) *PluginCache {
	return NewPluginCacheWithCapacity(ttl, DefaultCacheMaxEntries)
}

// NewPluginCacheWithCapacity 创建指定容量的插件缓存
func NewPluginCacheWithCapacity(ttl time.Duration, maxEntries int) *PluginCache {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheMaxEntries
	}
	pc := &PluginCache{
		cache:      make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: maxEntries,
		ttl:        ttl,
		stopChan:   make(chan struct{}),
	}
	// 启动清理过期缓存的goroutine
	go pc.cleanup()
	return pc
}

// Stop 停止缓存清理协程（幂等操作，sync.Once 保护 REQ-P1-02）
func (pc *PluginCache) Stop() {
	if pc != nil {
		pc.stopOnce.Do(func() {
			close(pc.stopChan)
		})
	}
}

// Get 获取缓存结果（兼容 chain.PluginCacheIface）
func (pc *PluginCache) Get(key string) (interface{}, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	elem, exists := pc.cache[key]
	if !exists {
		return nil, false
	}

	node := elem.Value.(*cacheNode)
	if time.Now().After(node.result.Expire) {
		delete(pc.cache, key)
		pc.lruList.Remove(elem)
		return nil, false
	}

	// 命中后移至 LRU 头部
	pc.lruList.MoveToFront(elem)
	return node.result.Data, true
}

// Set 设置缓存结果（兼容 chain.PluginCacheIface，LRU 淘汰保证内存有界）
func (pc *PluginCache) Set(key string, data interface{}) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	var dataMap map[string]interface{}
	if dm, ok := data.(map[string]interface{}); ok {
		dataMap = dm
	}

	res := &PluginResult{
		Success: true,
		Error:   nil,
		Data:    dataMap,
		Expire:  time.Now().Add(pc.ttl),
	}

	if elem, exists := pc.cache[key]; exists {
		node := elem.Value.(*cacheNode)
		node.result = res
		pc.lruList.MoveToFront(elem)
		return
	}

	// 达到容量上限：淘汰最久未访问 (LRU) 的缓存项
	for pc.lruList.Len() >= pc.maxEntries && pc.lruList.Len() > 0 {
		oldest := pc.lruList.Back()
		if oldest != nil {
			node := oldest.Value.(*cacheNode)
			delete(pc.cache, node.key)
			pc.lruList.Remove(oldest)
		}
	}

	node := &cacheNode{
		key:    key,
		result: res,
	}
	elem := pc.lruList.PushFront(node)
	pc.cache[key] = elem
}

// Len 返回当前缓存条目数
func (pc *PluginCache) Len() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return len(pc.cache)
}

// cleanup 清理过期缓存
func (pc *PluginCache) cleanup() {
	ticker := time.NewTicker(pc.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-pc.stopChan:
			return
		case <-ticker.C:
			pc.mu.Lock()
			now := time.Now()
			var next *list.Element
			for elem := pc.lruList.Back(); elem != nil; elem = next {
				next = elem.Prev()
				node := elem.Value.(*cacheNode)
				if now.After(node.result.Expire) {
					delete(pc.cache, node.key)
					pc.lruList.Remove(elem)
				}
			}
			pc.mu.Unlock()
		}
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
	// 序列化为JSON
	bytes, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	// 计算MD5哈希
	hash := md5.Sum(bytes)
	return hex.EncodeToString(hash[:])
}
