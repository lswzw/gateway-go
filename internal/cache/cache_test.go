package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLRU_EvictionAndTouch(t *testing.T) {
	c := NewLRU[int](2)

	c.Put("a", 1, 0)
	c.Put("b", 2, 0)

	// 访问 a 使其成为最近使用，随后插入 c 应淘汰 b
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a present")
	}
	c.Put("c", 3, 0)

	if _, ok := c.Get("b"); ok {
		t.Error("b 应被淘汰")
	}
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("a 应保留，实际 %v %v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Errorf("c 应存在，实际 %v %v", v, ok)
	}
	if c.Len() != 2 {
		t.Errorf("容量上限失效，Len=%d", c.Len())
	}
}

func TestLRU_TTLExpiry(t *testing.T) {
	c := NewLRU[string](10)
	base := time.Now()
	c.now = func() time.Time { return base }

	c.Put("k", "v", time.Minute)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("未到期应命中")
	}

	// 前进 2 分钟后应过期
	c.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := c.Get("k"); ok {
		t.Error("已过期条目不应命中")
	}
	if c.Len() != 0 {
		t.Errorf("过期条目应被清理，Len=%d", c.Len())
	}
}

func TestLRU_UpdateAndPurge(t *testing.T) {
	c := NewLRU[string](10)
	c.Put("k", "v1", 0)
	c.Put("k", "v2", 0)

	if v, _ := c.Get("k"); v != "v2" {
		t.Errorf("重复写入应覆盖，实际 %s", v)
	}
	if c.Len() != 1 {
		t.Errorf("重复写入不应新增条目，Len=%d", c.Len())
	}

	c.Purge()
	if c.Len() != 0 {
		t.Errorf("Purge 后应为空，Len=%d", c.Len())
	}
}

func TestLRU_DefaultCapacity(t *testing.T) {
	c := NewLRU[bool](0)
	if c.Capacity() != DefaultCapacity {
		t.Errorf("expected %d, got %d", DefaultCapacity, c.Capacity())
	}
}

func TestLRU_ConcurrentAccess(t *testing.T) {
	c := NewLRU[int](64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := string(rune('a' + (worker+j)%32))
				c.Put(key, j, time.Second)
				_, _ = c.Get(key)
			}
		}(i)
	}
	wg.Wait()

	if c.Len() > c.Capacity() {
		t.Errorf("Len %d 超过容量 %d", c.Len(), c.Capacity())
	}
}

func TestGroup_DoMergesConcurrentCalls(t *testing.T) {
	var g Group
	var calls int32

	start := make(chan struct{})
	fn := func() (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	}

	var wg sync.WaitGroup
	results := make([]interface{}, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			v, err, _ := g.Do("same-key", fn)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[idx] = v
		}(i)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("并发同 key 应只执行一次，实际 %d 次", got)
	}
	for i, v := range results {
		if v != "ok" {
			t.Errorf("第 %d 个调用结果异常: %v", i, v)
		}
	}
}

func TestGroup_DoPropagatesError(t *testing.T) {
	var g Group
	wantErr := errors.New("boom")

	_, err, _ := g.Do("k", func() (interface{}, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	// 失败后 key 必须被清理，后续调用可以重新执行
	var calls int32
	_, _, _ = g.Do("k", func() (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})
	if atomic.LoadInt32(&calls) != 1 {
		t.Error("前一次失败不应污染后续调用")
	}
}

func TestGroup_DoRecoversFromPanic(t *testing.T) {
	var g Group
	done := make(chan struct{})

	go func() {
		defer close(done)
		// 模拟上层 recover：panic 会原样抛出，但 Group 必须已清理 key
		defer func() { _ = recover() }()
		_, _, _ = g.Do("panic-key", func() (interface{}, error) { panic("boom") })
	}()

	select {
	case <-done:
		// panic 被原样抛出到该 goroutine，但 key 已被清理
	case <-time.After(2 * time.Second):
		t.Fatal("panic 后 Group.Do 挂死")
	}

	var calls int32
	_, _, _ = g.Do("panic-key", func() (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	})
	if atomic.LoadInt32(&calls) != 1 {
		t.Error("panic 后 key 未被清理")
	}
}
