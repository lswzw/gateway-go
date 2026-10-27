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

// 连接池默认容量。
//
// MaxIdleConnsPerHost 必须覆盖常见峰值并发：小于并发量时，超额的连接在请求结束后
// 无法回池而被立即关闭，下一次请求只能重新 dial，形成持续连接 churn。
// 实测（2026-10，4C 网关、800 并发）：旧值 100 时网关→后端 TIME_WAIT 堆到 28,131
// 个，把容器可用本地端口（28,231 个）全部占满，吞吐从 27k RPS 崩到 2.1k RPS，
// 并出现 502 与客户端超时；放大到 2048 后同一压测 TIME_WAIT 归零、吞吐 27k RPS。
const (
	// DefaultMaxIdleConns 所有 host 的空闲连接总上限
	DefaultMaxIdleConns = 4096
	// DefaultMaxIdleConnsPerHost 单个 host（上游）的空闲连接上限，需 ≥ 峰值并发
	DefaultMaxIdleConnsPerHost = 2048
)

// NewTransport 创建符合生产级连接池配置规范的 http.Transport 实例
// 依据 REQ-P1-01：
// MaxIdleConns: DefaultMaxIdleConns (4096)
// MaxIdleConnsPerHost: DefaultMaxIdleConnsPerHost (2048)
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
		MaxIdleConns:          DefaultMaxIdleConns,
		MaxIdleConnsPerHost:   DefaultMaxIdleConnsPerHost,
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
