package ipwhitelist

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

func TestIPWhitelist_SpoofedXFFRejected(t *testing.T) {
	// Ensure default trusted proxies is cleared (no trusted proxies configured)
	netutil.SetDefaultTrustedProxies(nil)

	plugin := New()
	cfg := map[string]interface{}{
		"ip_whitelist": []interface{}{"10.1.2.3"},
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("failed to init plugin: %v", err)
	}

	// Client sends spoofed XFF: 10.1.2.3, but RemoteAddr is 192.168.1.50
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/secure", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	req.Header.Set("X-Forwarded-For", "10.1.2.3")
	c.Request = req

	err := plugin.Execute(c)
	if err != nil {
		t.Fatalf("expected nil error per plugin contract, got: %v", err)
	}
	if !c.IsAborted() {
		t.Fatalf("expected context to be aborted for rejected request")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got status code %d", w.Code)
	}
}

func TestIPWhitelist_TrustedProxyAllowed(t *testing.T) {
	netutil.SetDefaultTrustedProxies([]string{"10.0.0.1"})
	defer netutil.SetDefaultTrustedProxies(nil)

	plugin := New()
	cfg := map[string]interface{}{
		"ip_whitelist": []interface{}{"203.0.113.195"},
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("failed to init plugin: %v", err)
	}

	// Request from trusted proxy forwarding allowed IP
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/secure", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")
	c.Request = req

	if err := plugin.Execute(c); err != nil {
		t.Fatalf("expected allowed access through trusted proxy, got: %v", err)
	}
	if w.Code == http.StatusForbidden {
		t.Fatalf("expected allowed access, but got 403")
	}
}

func TestIPWhitelist_UntrustedProxyWithAllowedXFFRejected(t *testing.T) {
	netutil.SetDefaultTrustedProxies([]string{"10.0.0.1"})
	defer netutil.SetDefaultTrustedProxies(nil)

	plugin := New()
	cfg := map[string]interface{}{
		"ip_whitelist": []interface{}{"203.0.113.195"},
	}
	if err := plugin.Init(cfg); err != nil {
		t.Fatalf("failed to init plugin: %v", err)
	}

	// Request from UNTRUSTED proxy forwarding allowed IP
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/secure", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")
	c.Request = req

	err := plugin.Execute(c)
	if err != nil {
		t.Fatalf("expected nil error per plugin contract, got: %v", err)
	}
	if !c.IsAborted() {
		t.Fatalf("expected context to be aborted for rejected request")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", w.Code)
	}
}
