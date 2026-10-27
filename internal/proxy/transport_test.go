package proxy

import (
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
