package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

// TransportPool 维护 per-route (或 per-target host) 缓存的 http.Transport 连接池
type TransportPool struct {
	mu         sync.RWMutex
	transports map[string]*http.Transport
}

// NewTransportPool 创建 Transport 连接池管理器
func NewTransportPool() *TransportPool {
	return &TransportPool{
		transports: make(map[string]*http.Transport),
	}
}

// NewTransport 创建符合生产级连接池配置规范的 http.Transport 实例
// 依据 REQ-P1-01：
// MaxIdleConns: 1000
// MaxIdleConnsPerHost: 100
// IdleConnTimeout: 90 * time.Second
// ResponseHeaderTimeout: 30 * time.Second
// TLSHandshakeTimeout: 10 * time.Second
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// GetTransport 获取指定 key 对应的缓存 Transport，若不存在则创建并缓存
func (p *TransportPool) GetTransport(key string) *http.Transport {
	if key == "" {
		key = "default"
	}

	p.mu.RLock()
	t, ok := p.transports[key]
	p.mu.RUnlock()
	if ok {
		return t
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 二次检查
	if t, ok = p.transports[key]; ok {
		return t
	}

	t = NewTransport()
	p.transports[key] = t
	return t
}

// CloseIdleConnections 关闭池中所有 Transport 的空闲连接
func (p *TransportPool) CloseIdleConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, t := range p.transports {
		t.CloseIdleConnections()
	}
}

// Reset 清空并关闭所有缓存的 Transport
func (p *TransportPool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, t := range p.transports {
		t.CloseIdleConnections()
	}
	p.transports = make(map[string]*http.Transport)
}
