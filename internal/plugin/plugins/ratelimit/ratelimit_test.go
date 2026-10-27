package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway-go/internal/netutil"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRateLimit_IPBasedWithTrustedProxies(t *testing.T) {
	// Set default trusted proxy to 10.0.0.1
	netutil.SetDefaultTrustedProxies([]string{"10.0.0.1"})
	defer netutil.SetDefaultTrustedProxies(nil)

	plugin := New()
	cfg := map[string]interface{}{
		"ip_based":            true,
		"requests_per_second": 0.1,
		"burst":               1,
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("failed to init ratelimit plugin: %v", err)
	}

	// 1. First request from client 203.0.113.1 via trusted proxy 10.0.0.1
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	req1, _ := http.NewRequest("GET", "/api/data", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	req1.Header.Set("X-Forwarded-For", "203.0.113.1")
	c1.Request = req1

	if err := plugin.Execute(c1); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first request should not be rate limited")
	}

	// 2. Second request immediately from the SAME client 203.0.113.1 should exceed capacity of 1
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	req2, _ := http.NewRequest("GET", "/api/data", nil)
	req2.RemoteAddr = "10.0.0.1:5678"
	req2.Header.Set("X-Forwarded-For", "203.0.113.1")
	c2.Request = req2

	_ = plugin.Execute(c2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from same IP should be rate limited with 429, got %d", w2.Code)
	}

	// 3. Request from a DIFFERENT client 203.0.113.2 via trusted proxy 10.0.0.1 should succeed
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	req3, _ := http.NewRequest("GET", "/api/data", nil)
	req3.RemoteAddr = "10.0.0.1:9999"
	req3.Header.Set("X-Forwarded-For", "203.0.113.2")
	c3.Request = req3

	if err := plugin.Execute(c3); err != nil {
		t.Fatalf("third request from different IP failed: %v", err)
	}
	if w3.Code == http.StatusTooManyRequests {
		t.Fatalf("request from different IP should not be rate limited")
	}
}

func TestRateLimit_IPBasedWithoutTrustedProxies(t *testing.T) {
	// No trusted proxies configured
	netutil.SetDefaultTrustedProxies(nil)

	plugin := New()
	cfg := map[string]interface{}{
		"ip_based":            true,
		"requests_per_second": 0.1,
		"burst":               1,
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("failed to init ratelimit plugin: %v", err)
	}

	// Request from remote IP 192.168.1.100 with spoofed XFF 203.0.113.1
	key1 := plugin.getLimitKey(&gin.Context{
		Request: &http.Request{
			RemoteAddr: "192.168.1.100:1234",
			Header: http.Header{
				"X-Forwarded-For": []string{"203.0.113.1"},
			},
		},
	})

	// Key must be RemoteAddr IP, NOT the spoofed XFF!
	if key1 != "192.168.1.100" {
		t.Fatalf("expected limit key to be RemoteAddr IP 192.168.1.100, got %s", key1)
	}
}

// newTestCtx 构造一个带请求的 gin 测试上下文
func newTestCtx(method, path, remoteAddr string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(method, path, nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

// bucketOf 读取指定键对应的令牌桶（同包内部断言用）
func bucketOf(p *RateLimitPlugin, key string) *TokenBucket {
	p.mu.Lock()
	defer p.mu.Unlock()
	if elem, ok := p.buckets[key]; ok {
		return elem.Value.(*bucketNode).bucket
	}
	return nil
}

// TestRateLimit_NumericConfigHonored 回归测试：整数/字符串形式的 requests_per_second
// 必须生效。历史缺陷只做 float64 断言，config.yaml 里的 requests_per_second: 100
// 会被静默回落成 10 rps。
func TestRateLimit_NumericConfigHonored(t *testing.T) {
	cases := []struct {
		name      string
		rps       interface{}
		burst     interface{}
		wantRate  float64
		wantBurst int64
	}{
		{"yaml int", 100, 200, 100, 200},
		{"yaml float", 100.0, 200, 100, 200},
		{"int64", int64(250), int64(300), 250, 300},
		{"string", "50", "60", 50, 60},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New()
			cfg := map[string]interface{}{
				"requests_per_second": tc.rps,
				"burst":               tc.burst,
				"dimension":           "global",
			}
			if err := p.Init(cfg); err != nil {
				t.Fatalf("unexpected init error: %v", err)
			}

			c, w := newTestCtx("GET", "/api/data", "192.168.1.1:1111", nil)
			if err := p.Execute(c); err != nil {
				t.Fatalf("unexpected execute error: %v", err)
			}
			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("first request should not be limited")
			}

			b := bucketOf(p, "global")
			if b == nil {
				t.Fatal("expected bucket to be created")
			}
			if b.rate != tc.wantRate {
				t.Errorf("expected rate %v, got %v（数值类型断言失败会静默回落成 %v）",
					tc.wantRate, b.rate, DefaultRate)
			}
			if b.capacity != tc.wantBurst {
				t.Errorf("expected capacity %d, got %d", tc.wantBurst, b.capacity)
			}
			if b.tokens != tc.wantBurst-1 {
				t.Errorf("expected tokens %d after one request, got %d", tc.wantBurst-1, b.tokens)
			}
		})
	}
}

// TestRateLimit_Dimension 覆盖 dimension: ip / user / global / path 的建桶行为
func TestRateLimit_Dimension(t *testing.T) {
	netutil.SetDefaultTrustedProxies(nil)

	// 1. ip 维度：不同客户端 IP 各自建桶，同一 IP 共享
	ipPlugin := New()
	if err := ipPlugin.Init(map[string]interface{}{
		"dimension": "ip", "requests_per_second": 0.01, "burst": 1,
	}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	c1, w1 := newTestCtx("GET", "/same/path", "10.0.0.1:1000", nil)
	if err := ipPlugin.Execute(c1); err != nil || w1.Code == http.StatusTooManyRequests {
		t.Fatalf("ip#1 first request should pass, code=%d err=%v", w1.Code, err)
	}
	c2, w2 := newTestCtx("GET", "/same/path", "10.0.0.2:1000", nil)
	if err := ipPlugin.Execute(c2); err != nil || w2.Code == http.StatusTooManyRequests {
		t.Fatalf("ip#2 first request should pass（不同 IP 不应共享桶）, code=%d err=%v", w2.Code, err)
	}
	c3, w3 := newTestCtx("GET", "/same/path", "10.0.0.1:2000", nil)
	_ = ipPlugin.Execute(c3)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("ip#1 second request should be limited, got %d", w3.Code)
	}
	if got := ipPlugin.BucketCount(); got != 2 {
		t.Errorf("expected 2 buckets for 2 IPs, got %d", got)
	}

	// 2. global 维度：不同路径 + 不同 IP 共享同一个桶
	globalPlugin := New()
	if err := globalPlugin.Init(map[string]interface{}{
		"dimension": "global", "requests_per_second": 0.01, "burst": 1,
	}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	g1, gw1 := newTestCtx("GET", "/a", "10.0.1.1:1000", nil)
	_ = globalPlugin.Execute(g1)
	g2, gw2 := newTestCtx("GET", "/b", "10.0.1.2:1000", nil)
	_ = globalPlugin.Execute(g2)
	if gw1.Code == http.StatusTooManyRequests {
		t.Fatal("global#1 should pass")
	}
	if gw2.Code != http.StatusTooManyRequests {
		t.Fatalf("global 维度下第二个请求应被限流，实际 %d", gw2.Code)
	}
	if got := globalPlugin.BucketCount(); got != 1 {
		t.Errorf("expected 1 bucket in global dimension, got %d", got)
	}

	// 3. user 维度：按请求头区分用户
	userPlugin := New()
	if err := userPlugin.Init(map[string]interface{}{
		"dimension": "user", "requests_per_second": 0.01, "burst": 1,
		"user_header": "X-User-Id",
	}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	u1, uw1 := newTestCtx("GET", "/api", "10.0.2.1:1000", map[string]string{"X-User-Id": "u-1"})
	_ = userPlugin.Execute(u1)
	u2, uw2 := newTestCtx("GET", "/api", "10.0.2.1:1000", map[string]string{"X-User-Id": "u-2"})
	_ = userPlugin.Execute(u2)
	u3, uw3 := newTestCtx("GET", "/api", "10.0.2.1:1000", map[string]string{"X-User-Id": "u-1"})
	_ = userPlugin.Execute(u3)
	if uw1.Code == http.StatusTooManyRequests || uw2.Code == http.StatusTooManyRequests {
		t.Fatal("不同用户首次请求都应放行")
	}
	if uw3.Code != http.StatusTooManyRequests {
		t.Fatalf("同一用户第二次请求应被限流，实际 %d", uw3.Code)
	}

	// 4. 未配置 dimension / ip_based：保持历史行为（按路径）
	pathPlugin := New()
	if err := pathPlugin.Init(map[string]interface{}{"requests_per_second": 100}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	p1, _ := newTestCtx("GET", "/p1", "10.0.3.1:1000", nil)
	_ = pathPlugin.Execute(p1)
	p2, _ := newTestCtx("GET", "/p2", "10.0.3.1:1000", nil)
	_ = pathPlugin.Execute(p2)
	if got := pathPlugin.BucketCount(); got != 2 {
		t.Errorf("expected 2 buckets for 2 paths (path dimension), got %d", got)
	}
}
