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
	if t1.MaxIdleConns != 1000 {
		t.Errorf("expected MaxIdleConns 1000, got %d", t1.MaxIdleConns)
	}
	if t1.MaxIdleConnsPerHost != 100 {
		t.Errorf("expected MaxIdleConnsPerHost 100, got %d", t1.MaxIdleConnsPerHost)
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
