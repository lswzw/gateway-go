package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sort"
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
	// DefaultMaxConnsPerHost 单个上游的连接总数上限（含活跃连接）。
	// 不设上限时慢上游可以无限占用连接与 fd，这里显式封顶，让 2048 这个数字可解释：
	// 单副本 fd 预算 ≈ 上游 host 数 × DefaultMaxConnsPerHost + 监听/管理面连接。
	DefaultMaxConnsPerHost = 2048
	// DefaultIdleConnTimeout 空闲连接回收时间
	DefaultIdleConnTimeout = 90 * time.Second
)

// NewTransport 创建符合生产级连接池配置规范的 http.Transport 实例
// 依据 REQ-P1-01：
// MaxIdleConns: DefaultMaxIdleConns (4096)
// MaxIdleConnsPerHost: DefaultMaxIdleConnsPerHost (2048)
// MaxConnsPerHost: DefaultMaxConnsPerHost (2048)
// IdleConnTimeout: DefaultIdleConnTimeout (90s)
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
		MaxConnsPerHost:       DefaultMaxConnsPerHost,
		IdleConnTimeout:       DefaultIdleConnTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// PoolKey 返回上游连接池的键：scheme://host。
//
// 历史实现用路由名作键，导致两条路由指向同一上游时各自建池（连接无法共享），
// 且路由改名/删除后旧池永久驻留。改为按上游 host 共享后，路由级差异
// （timeout / retries）仍保留在请求层，不受影响。
func PoolKey(u *url.URL) string {
	if u == nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
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

// Reconcile 关闭并移除不再被引用的上游连接池，返回被回收的池数量。
//
// 锁内只摘除 map 条目并快照待关闭的 Transport，真正的关闭放到锁外，
// 避免 CloseIdleConnections 阻塞热路径上的 GetTransport。判定依据是调用方
// 传入的 activeKeys（当前配置中仍存在的上游），因此刚被新请求重建的池不会被误删。
func (p *TransportPool) Reconcile(activeKeys []string) int {
	active := make(map[string]struct{}, len(activeKeys))
	for _, k := range activeKeys {
		if k == "" {
			continue
		}
		active[k] = struct{}{}
	}

	p.mu.Lock()
	stale := make([]*http.Transport, 0, len(p.transports))
	for k, t := range p.transports {
		if _, keep := active[k]; keep {
			continue
		}
		stale = append(stale, t)
		delete(p.transports, k)
	}
	p.mu.Unlock()

	for _, t := range stale {
		t.CloseIdleConnections()
	}
	return len(stale)
}

// Keys 返回当前池中的键列表（按字典序，供日志与排障）
func (p *TransportPool) Keys() []string {
	p.mu.RLock()
	keys := make([]string, 0, len(p.transports))
	for k := range p.transports {
		keys = append(keys, k)
	}
	p.mu.RUnlock()

	sort.Strings(keys)
	return keys
}
