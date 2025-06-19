package router

import (
	"sync"
)

// RouteCache 路由缓存
// 采用简单的LRU策略
// key为请求路径，value为路由定义
// 生产环境可用更高效的LRU库替换

type RouteCache struct {
	cache map[string]*RouteDefinition
	mu    sync.RWMutex
	size  int
}

// NewRouteCache 创建路由缓存
func NewRouteCache(size int) *RouteCache {
	return &RouteCache{
		cache: make(map[string]*RouteDefinition, size),
		size:  size,
	}
}

// Get 获取缓存
func (rc *RouteCache) Get(key string) (*RouteDefinition, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	route, exists := rc.cache[key]
	return route, exists
}

// Set 设置缓存
func (rc *RouteCache) Set(key string, route *RouteDefinition) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// 简单LRU：超出容量时删除第一个
	if len(rc.cache) >= rc.size {
		for k := range rc.cache {
			delete(rc.cache, k)
			break
		}
	}

	rc.cache[key] = route
}
