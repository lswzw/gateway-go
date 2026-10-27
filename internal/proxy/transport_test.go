package proxy

import (
	"net/url"
	"testing"
	"time"
)

func TestTransportPool(t *testing.T) {
	pool := NewTransportPool()

	t1 := pool.GetTransport("route-1")
	if t1 == nil {
		t.Fatal("expected non-nil transport")
	}

	// 验证配置符合 REQ-P1-01 规范
	if t1.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("expected MaxIdleConns %d, got %d", DefaultMaxIdleConns, t1.MaxIdleConns)
	}
	if t1.MaxIdleConnsPerHost != DefaultMaxIdleConnsPerHost {
		t.Errorf("expected MaxIdleConnsPerHost %d, got %d", DefaultMaxIdleConnsPerHost, t1.MaxIdleConnsPerHost)
	}
	if t1.IdleConnTimeout != 90*time.Second {
		t.Errorf("expected IdleConnTimeout 90s, got %v", t1.IdleConnTimeout)
	}
	if t1.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("expected ResponseHeaderTimeout 30s, got %v", t1.ResponseHeaderTimeout)
	}
	if t1.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("expected TLSHandshakeTimeout 10s, got %v", t1.TLSHandshakeTimeout)
	}

	// 相同 key 获取同一实例
	t2 := pool.GetTransport("route-1")
	if t1 != t2 {
		t.Error("expected same transport instance for same key")
	}

	// 不同 key 获取不同实例
	t3 := pool.GetTransport("route-2")
	if t1 == t3 {
		t.Error("expected different transport instances for different keys")
	}

	// 验证清理方法不 panic
	pool.CloseIdleConnections()
	pool.Reset()

	t4 := pool.GetTransport("route-1")
	if t4 == t1 {
		t.Error("expected new transport instance after reset")
	}
}

// TestTransport_IdlePoolCoversConcurrency 回归测试：单 host 的 idle 连接池必须覆盖常见并发，
// 否则高并发下退化为"每次请求重新 dial"，产生 TIME_WAIT churn 并耗尽本地端口
// （实测 800 并发、池=100 时吞吐从 27k 崩到 2.1k RPS）。
func TestTransport_IdlePoolCoversConcurrency(t *testing.T) {
	const minConcurrency = 1024

	tr := NewTransport()
	if tr.MaxIdleConnsPerHost < minConcurrency {
		t.Errorf("MaxIdleConnsPerHost=%d 过小（< %d），高并发下会发生连接 churn",
			tr.MaxIdleConnsPerHost, minConcurrency)
	}
	if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConns=%d 应不小于 MaxIdleConnsPerHost=%d",
			tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
	if tr.DisableKeepAlives {
		t.Error("DisableKeepAlives 必须为 false，否则每个请求都会新建连接")
	}
}

// TestPoolKey 池键必须按上游 host 归并（而不是按路由名）
func TestPoolKey(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"http://10.0.0.1:8080/api", "http://10.0.0.1:8080"},
		{"https://backend.svc:443/", "https://backend.svc:443"},
		{"http://backend.svc/", "http://backend.svc"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.raw, err)
		}
		if got := PoolKey(u); got != tc.want {
			t.Errorf("PoolKey(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if got := PoolKey(nil); got != "" {
		t.Errorf("nil url 应返回空键，实际 %q", got)
	}
}

// TestTransportPool_Reconcile 回收未被引用的池，保留仍在使用中的池实例
func TestTransportPool_Reconcile(t *testing.T) {
	pool := NewTransportPool()

	keep := pool.GetTransport("http://a:80")
	stale := pool.GetTransport("http://b:80")

	if released := pool.Reconcile([]string{"http://a:80"}); released != 1 {
		t.Fatalf("expected 1 released pool, got %d", released)
	}
	if again := pool.GetTransport("http://a:80"); again != keep {
		t.Error("仍被引用的池必须保留原实例")
	}
	// b 的池已被摘除，再次获取应创建新实例
	if reborn := pool.GetTransport("http://b:80"); reborn == stale {
		t.Error("被回收的池不应复用旧实例")
	}
	if keys := pool.Keys(); len(keys) != 2 {
		t.Errorf("expected 2 pools after re-create, got %v", keys)
	}
}

// TestTransportPool_ReconcileWithEmptyActive 所有上游都下线时回收全部池
func TestTransportPool_ReconcileWithEmptyActive(t *testing.T) {
	pool := NewTransportPool()
	pool.GetTransport("http://a:80")
	pool.GetTransport("http://b:80")

	if released := pool.Reconcile(nil); released != 2 {
		t.Fatalf("expected 2 released, got %d", released)
	}
	if keys := pool.Keys(); len(keys) != 0 {
		t.Errorf("expected empty pool, got %v", keys)
	}
}
