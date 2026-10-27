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
