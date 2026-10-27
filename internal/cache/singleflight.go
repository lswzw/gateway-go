package cache

import "sync"

type call struct {
	wg   sync.WaitGroup
	val  interface{}
	err  error
	dups int
}

// Group 把同一 key 的并发调用合并成一次执行，避免冷缓存下同一令牌
// 的并发请求同时打到下游（认证服务）。仅进程内合并，不跨副本。
type Group struct {
	mu    sync.Mutex
	calls map[string]*call
}

// Do 执行 fn；相同 key 的并发调用共享同一次结果。
// 返回的 shared 表示当前调用是否复用了别人的执行结果。
func (g *Group) Do(key string, fn func() (interface{}, error)) (val interface{}, err error, shared bool) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call)
	}
	if c, ok := g.calls[key]; ok {
		c.dups++
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := new(call)
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	func() {
		// 无论 fn 是否 panic 都要唤醒等待者并清理条目，否则该 key 永久挂起
		defer func() {
			c.wg.Done()
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
		}()
		c.val, c.err = fn()
	}()

	return c.val, c.err, false
}
