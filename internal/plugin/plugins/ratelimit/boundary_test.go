package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gateway-go/internal/testutil"
)

func TestTokenBucket_BoundaryAndRefill(t *testing.T) {
	// Create a token bucket with capacity 3 and refill rate 5 tokens/second
	tb := &TokenBucket{
		rate:       5.0,
		capacity:   3,
		tokens:     3,
		lastRefill: time.Now().UnixNano(),
	}

	// Consume token 1
	if !tb.allow() {
		t.Fatalf("expected token 1 to be allowed")
	}
	// Consume token 2
	if !tb.allow() {
		t.Fatalf("expected token 2 to be allowed")
	}
	// Consume token 3 (reaches boundary 0)
	if !tb.allow() {
		t.Fatalf("expected token 3 to be allowed")
	}

	// 4th request immediately should fail (0 tokens left)
	if tb.allow() {
		t.Fatalf("expected 4th request to be rejected at boundary")
	}

	// Sleep 250ms -> should refill ~1 token (5 tokens/sec * 0.25s = 1.25)
	time.Sleep(250 * time.Millisecond)
	if !tb.allow() {
		t.Fatalf("expected request allowed after token refill")
	}

	// Wait long enough (1s) so tokens attempt to exceed capacity
	time.Sleep(1 * time.Second)
	tb.allow()
	// Tokens should never exceed capacity
	tb.mu.RLock()
	if tb.tokens > tb.capacity {
		t.Errorf("tokens (%d) exceeded capacity (%d)", tb.tokens, tb.capacity)
	}
	tb.mu.RUnlock()
}

func TestRateLimit_PathBasedAndInit(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	// Invalid config
	err := plugin.Init("invalid_config")
	if err == nil {
		t.Fatalf("expected error for invalid config, got nil")
	}

	// Valid path-based config
	cfg := map[string]interface{}{
		"ip_based":            false,
		"requests_per_second": 10.0,
		"burst":               2,
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}

	w1 := httptest.NewRecorder()
	c1, _ := testutil.NewTestContext(w1)
	req1, _ := http.NewRequest("GET", "/api/v1/orders", nil)
	c1.Request = req1

	if err := plugin.Execute(c1); err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	w2 := httptest.NewRecorder()
	c2, _ := testutil.NewTestContext(w2)
	req2, _ := http.NewRequest("GET", "/api/v1/orders", nil)
	c2.Request = req2
	_ = plugin.Execute(c2)

	// 3rd request to same path should be rate limited
	w3 := httptest.NewRecorder()
	c3, _ := testutil.NewTestContext(w3)
	req3, _ := http.NewRequest("GET", "/api/v1/orders", nil)
	c3.Request = req3
	_ = plugin.Execute(c3)

	testutil.AssertStatus(t, w3, http.StatusTooManyRequests)
	testutil.AssertJSON(t, w3.Body, map[string]interface{}{
		"code":    429,
		"message": "请求过于频繁",
	})
}
