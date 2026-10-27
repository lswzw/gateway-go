package interface_auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// startAuthServer 启动一个固定状态码的认证服务，并统计真实收到的请求数
func startAuthServer(t *testing.T, status int, delay time.Duration) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newPluginWithAuth(t *testing.T, srv *httptest.Server, extra map[string]interface{}) *Plugin {
	t.Helper()

	cfg := map[string]interface{}{
		"consumers": map[string]interface{}{
			"host":      strings.TrimPrefix(srv.URL, "http://"),
			"auth_api":  "/verify",
			"auth_mode": "http_status",
		},
	}
	for k, v := range extra {
		cfg[k] = v
	}

	p := New()
	t.Cleanup(func() { _ = p.Stop() })
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return p
}

// TestDenyResultNotCached 拒绝结论不进入缓存：认证服务抖动导致的 401 不应在 TTL 内持续生效
func TestDenyResultNotCached(t *testing.T) {
	srv, calls := startAuthServer(t, http.StatusUnauthorized, 0)
	p := newPluginWithAuth(t, srv, map[string]interface{}{"cache_ttl": 60})

	for i := 0; i < 3; i++ {
		c, w := setupTestContext("token-deny", "/protected")
		if err := p.Execute(c); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	}

	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("拒绝结论不应被缓存，期望 3 次外呼，实际 %d 次", got)
	}
	if p.CacheSize() != 0 {
		t.Errorf("拒绝结论不应写入缓存，CacheSize=%d", p.CacheSize())
	}
}

// TestAllowResultCached 放行结论命中缓存后不再外呼
func TestAllowResultCached(t *testing.T) {
	srv, calls := startAuthServer(t, http.StatusOK, 0)
	p := newPluginWithAuth(t, srv, map[string]interface{}{"cache_ttl": 60})

	for i := 0; i < 3; i++ {
		c, w := setupTestContext("token-allow", "/protected")
		if err := p.Execute(c); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	}

	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("放行结论应命中缓存，期望 1 次外呼，实际 %d 次", got)
	}
	if p.CacheSize() != 1 {
		t.Errorf("expected 1 cache entry, got %d", p.CacheSize())
	}
}

// TestCacheKeyNamespacedByAuthService 同一令牌在不同认证服务下不得复用结论
func TestCacheKeyNamespacedByAuthService(t *testing.T) {
	srv1, calls1 := startAuthServer(t, http.StatusOK, 0)
	srv2, calls2 := startAuthServer(t, http.StatusOK, 0)

	p := newPluginWithAuth(t, srv1, map[string]interface{}{"cache_ttl": 60})
	c1, _ := setupTestContext("shared-token", "/protected")
	_ = p.Execute(c1)

	// 切换认证服务（模拟同一插件配置指向另一服务），令牌相同但缓存不应命中
	if err := p.Init(map[string]interface{}{
		"cache_ttl": 60,
		"consumers": map[string]interface{}{
			"host":      strings.TrimPrefix(srv2.URL, "http://"),
			"auth_api":  "/verify",
			"auth_mode": "http_status",
		},
	}); err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	c2, _ := setupTestContext("shared-token", "/protected")
	_ = p.Execute(c2)

	if got := atomic.LoadInt32(calls1); got != 1 {
		t.Errorf("第一个认证服务应被调用 1 次，实际 %d", got)
	}
	if got := atomic.LoadInt32(calls2); got != 1 {
		t.Errorf("换认证服务后必须重新校验，实际调用 %d 次", got)
	}
}

// TestUnavailableReturns503 认证服务异常时返回 503（而非 500），便于告警区分
func TestUnavailableReturns503(t *testing.T) {
	srv, _ := startAuthServer(t, http.StatusInternalServerError, 0)
	p := newPluginWithAuth(t, srv, map[string]interface{}{"cache_ttl": 60})

	c, w := setupTestContext("token-503", "/protected")
	if err := p.Execute(c); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Auth service call failed") {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

// TestBreakerOpensAfterConsecutiveFailures 连续失败 5 次后短路，不再逐请求等超时
func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	srv, calls := startAuthServer(t, http.StatusInternalServerError, 0)
	// cache_ttl=0 让每次请求都走外呼路径
	p := newPluginWithAuth(t, srv, map[string]interface{}{"cache_ttl": 0})

	for i := 0; i < failureThreshold; i++ {
		c, w := setupTestContext("token-breaker", "/protected")
		_ = p.Execute(c)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("第 %d 次应返回 503，实际 %d", i+1, w.Code)
		}
	}

	before := atomic.LoadInt32(calls)

	c, w := setupTestContext("token-breaker", "/protected")
	_ = p.Execute(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("短路期间应返回 503，实际 %d", w.Code)
	}
	if got := atomic.LoadInt32(calls); got != before {
		t.Errorf("短路期间不应再外呼，期望仍为 %d 次，实际 %d 次", before, got)
	}
}

// TestSingleflightMergesConcurrentMisses 冷缓存下同一令牌并发请求只外呼一次
func TestSingleflightMergesConcurrentMisses(t *testing.T) {
	srv, calls := startAuthServer(t, http.StatusOK, 80*time.Millisecond)
	p := newPluginWithAuth(t, srv, map[string]interface{}{"cache_ttl": 60})

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, w := setupTestContext("token-singleflight", "/protected")
			_ = p.Execute(c)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("并发同令牌应合并为 1 次外呼，实际 %d 次", got)
	}
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("第 %d 个并发请求状态码异常: %d", i, code)
		}
	}
}

// TestCacheCapacityRespected 容量上限生效，避免内存无界增长
func TestCacheCapacityRespected(t *testing.T) {
	srv, _ := startAuthServer(t, http.StatusOK, 0)
	p := newPluginWithAuth(t, srv, map[string]interface{}{
		"cache_ttl":         60,
		"cache_max_entries": 2,
	})

	for i := 0; i < 5; i++ {
		c, _ := setupTestContext("token-"+string(rune('a'+i)), "/protected")
		_ = p.Execute(c)
	}

	if p.CacheSize() > 2 {
		t.Errorf("容量上限失效，CacheSize=%d", p.CacheSize())
	}
}

// TestWildcardWhitelistEscapesRegexMeta 通配符白名单中的正则元字符必须被转义
func TestWildcardWhitelistEscapesRegexMeta(t *testing.T) {
	srv, calls := startAuthServer(t, http.StatusOK, 0)
	p := newPluginWithAuth(t, srv, map[string]interface{}{
		"cache_ttl":        0,
		"white_interfaces": []interface{}{"/a.b/*"},
	})

	// 命中白名单：不需要 token 也不外呼
	c1, w1 := setupTestContext("", "/a.b/1")
	_ = p.Execute(c1)
	if w1.Code != http.StatusOK || atomic.LoadInt32(calls) != 0 {
		t.Fatalf("白名单路径应直接放行，code=%d calls=%d", w1.Code, atomic.LoadInt32(calls))
	}

	// 未命中白名单：'.' 不应被当作正则通配，缺少 token 必须 401
	c2, w2 := setupTestContext("", "/axb/1")
	_ = p.Execute(c2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("非白名单路径应返回 401，实际 %d", w2.Code)
	}
}
