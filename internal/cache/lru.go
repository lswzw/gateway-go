// Package cache 提供网关进程内使用的缓存原语。
//
// 设计门禁（2026-10 评审结论）：本包只允许实现本地内存结构，禁止引入任何外部
// 共享存储（Redis / Memcached 等）及其客户端依赖，Makefile 的 guard 目标会检查
// go.mod。多副本部署时缓存不跨副本共享，"结论生效"的一致性上界由 TTL 保证——
// 即某个令牌被撤销后，最迟在 TTL 之后各副本都会重新校验。
package cache

import (
	"container/list"
	"sync"
	"time"
)

// DefaultCapacity 默认容量上限（条）
const DefaultCapacity = 20000

type entry[V any] struct {
	key      string
	value    V
	expireAt time.Time // 零值表示永不过期
}

// LRU 并发安全的定长 LRU 缓存，支持按条目设置 TTL。
type LRU[V any] struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[string]*list.Element
	now      func() time.Time
}

// NewLRU 创建容量为 capacity 的 LRU；capacity <= 0 时使用 DefaultCapacity。
func NewLRU[V any](capacity int) *LRU[V] {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &LRU[V]{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element, capacity),
		now:      time.Now,
	}
}

// Get 读取缓存；条目已过期时按未命中处理并删除。
func (c *LRU[V]) Get(key string) (V, bool) {
	var zero V

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	ent := elem.Value.(*entry[V])
	if !ent.expireAt.IsZero() && !c.now().Before(ent.expireAt) {
		c.ll.Remove(elem)
		delete(c.items, key)
		return zero, false
	}
	c.ll.MoveToFront(elem)
	return ent.value, true
}

// Put 写入缓存；ttl <= 0 表示永不过期。容量满时淘汰最久未使用条目。
func (c *LRU[V]) Put(key string, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expireAt time.Time
	if ttl > 0 {
		expireAt = c.now().Add(ttl)
	}

	if elem, ok := c.items[key]; ok {
		ent := elem.Value.(*entry[V])
		ent.value = value
		ent.expireAt = expireAt
		c.ll.MoveToFront(elem)
		return
	}

	elem := c.ll.PushFront(&entry[V]{key: key, value: value, expireAt: expireAt})
	c.items[key] = elem
	for c.ll.Len() > c.capacity {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*entry[V]).key)
		}
	}
}

// Delete 删除指定条目。
func (c *LRU[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.ll.Remove(elem)
		delete(c.items, key)
	}
}

// Len 返回当前条目数（含尚未清理的过期条目）。
func (c *LRU[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// Capacity 返回容量上限。
func (c *LRU[V]) Capacity() int {
	return c.capacity
}

// Purge 清空全部条目。
func (c *LRU[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ll.Init()
	c.items = make(map[string]*list.Element, c.capacity)
}
