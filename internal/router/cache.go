package router

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxCacheEntries 默认最大缓存条目数 (REQ-P1-03)
	DefaultMaxCacheEntries = 10000
	// DefaultCacheTTL 默认缓存TTL
	DefaultCacheTTL = 5 * time.Minute
)

// cacheItem 路由缓存条目
type cacheItem struct {
	key       string
	route     *RouteDefinition
	expiresAt time.Time
}

// RouteCache 路由缓存，基于 LRU 与 TTL 淘汰机制，并发安全 (REQ-P1-03)
// key 为 Method + Host + Path 的组合键，value 为匹配的路由定义
type RouteCache struct {
	mu         sync.RWMutex
	cache      map[string]*list.Element
	lruList    *list.List
	maxEntries int
	defaultTTL time.Duration
}

// NewRouteCache 创建指定容量的路由缓存 (若 size <= 0 则使用默认 10000)
func NewRouteCache(size int) *RouteCache {
	return NewRouteCacheWithTTL(size, DefaultCacheTTL)
}

// NewRouteCacheWithTTL 创建带有容量与默认 TTL 的路由缓存
func NewRouteCacheWithTTL(maxEntries int, ttl time.Duration) *RouteCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxCacheEntries
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &RouteCache{
		cache:      make(map[string]*list.Element, maxEntries),
		lruList:    list.New(),
		maxEntries: maxEntries,
		defaultTTL: ttl,
	}
}

// BuildRouteCacheKey 根据 HTTP 方法、主机与路径构建多维缓存键 (REQ-P1-03)
func BuildRouteCacheKey(method, host, path string) string {
	// 标准化方法与主机为小写/统一格式
	m := strings.ToUpper(strings.TrimSpace(method))
	h := strings.ToLower(strings.TrimSpace(host))
	// 剔除 host 中的端口号 (如 example.com:8080 -> example.com)
	if colonIdx := strings.IndexByte(h, ':'); colonIdx != -1 {
		h = h[:colonIdx]
	}
	p := strings.TrimSpace(path)
	if p == "" {
		p = "/"
	}
	return m + ":" + h + ":" + p
}

// BuildKey 实例方法构建多维缓存键
func (rc *RouteCache) BuildKey(method, host, path string) string {
	return BuildRouteCacheKey(method, host, path)
}

// Get 获取缓存条目，支持并发读（RLock）并校验 TTL (REQ-P1-03)
func (rc *RouteCache) Get(key string) (*RouteDefinition, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	elem, exists := rc.cache[key]
	if !exists {
		return nil, false
	}

	item := elem.Value.(*cacheItem)
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return nil, false
	}

	return item.route, true
}

// Set 设置缓存条目（使用默认 TTL），超出容量时进行 LRU 淘汰 (REQ-P1-03)
func (rc *RouteCache) Set(key string, route *RouteDefinition) {
	rc.SetWithTTL(key, route, rc.defaultTTL)
}

// SetWithTTL 设置带有独立 TTL 的缓存条目
func (rc *RouteCache) SetWithTTL(key string, route *RouteDefinition, ttl time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// 若已存在，则更新并移至头部
	if elem, exists := rc.cache[key]; exists {
		item := elem.Value.(*cacheItem)
		item.route = route
		item.expiresAt = expiresAt
		rc.lruList.MoveToFront(elem)
		return
	}

	// 若超出容量，淘汰最久未访问条目 (LRU)
	if rc.lruList.Len() >= rc.maxEntries {
		rc.evictOldest()
	}

	item := &cacheItem{
		key:       key,
		route:     route,
		expiresAt: expiresAt,
	}
	elem := rc.lruList.PushFront(item)
	rc.cache[key] = elem
}

// evictOldest 内部方法：淘汰队尾最旧元素 (调用方持有写锁)
func (rc *RouteCache) evictOldest() {
	elem := rc.lruList.Back()
	if elem != nil {
		rc.lruList.Remove(elem)
		item := elem.Value.(*cacheItem)
		delete(rc.cache, item.key)
	}
}

// Delete 删除指定缓存键
func (rc *RouteCache) Delete(key string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if elem, exists := rc.cache[key]; exists {
		rc.lruList.Remove(elem)
		delete(rc.cache, key)
	}
}

// Clear 清空所有缓存
func (rc *RouteCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.cache = make(map[string]*list.Element, rc.maxEntries)
	rc.lruList.Init()
}

// Len 返回当前有效缓存条目数
func (rc *RouteCache) Len() int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return len(rc.cache)
}

// MaxEntries 返回最大容量限制
func (rc *RouteCache) MaxEntries() int {
	return rc.maxEntries
}
