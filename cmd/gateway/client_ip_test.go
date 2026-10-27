package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/netutil"
	"gateway-go/internal/plugin/plugins/ipwhitelist"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestClientIP_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		xff            string
		xRealIP        string
		trustedProxies []string
		expectedIP     string
	}{
		// Case 1: No trusted proxies configured + spoofed X-Forwarded-For -> returns RemoteAddr
		{
			name:           "Case 1: No trusted proxies configured + spoofed XFF",
			remoteAddr:     "192.168.1.50:54321",
			xff:            "10.1.2.3",
			trustedProxies: nil,
			expectedIP:     "192.168.1.50",
		},
		{
			name:           "Case 1b: Empty slice trusted proxies + spoofed XFF",
			remoteAddr:     "192.168.1.50:54321",
			xff:            "10.1.2.3",
			xRealIP:        "10.1.2.4",
			trustedProxies: []string{},
			expectedIP:     "192.168.1.50",
		},
		// Case 2: Single trusted proxy + X-Forwarded-For -> returns client IP
		{
			name:           "Case 2a: Single trusted proxy exact IP + XFF",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "203.0.113.195",
			trustedProxies: []string{"10.0.0.1"},
			expectedIP:     "203.0.113.195",
		},
		{
			name:           "Case 2b: Single trusted proxy CIDR + XFF",
			remoteAddr:     "10.0.0.5:12345",
			xff:            "203.0.113.195",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "203.0.113.195",
		},
		{
			name:           "Case 2c: Untrusted remote IP sending XFF is ignored",
			remoteAddr:     "198.51.100.1:12345",
			xff:            "203.0.113.195",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "198.51.100.1",
		},
		// Case 3: Multi-hop XFF
		{
			name:           "Case 3a: Multi-hop XFF with trusted intermediate proxy",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "203.0.113.195, 10.0.0.2",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "203.0.113.195",
		},
		{
			name:           "Case 3b: Multi-hop XFF with spoofed prefix ignored",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "1.2.3.4, 203.0.113.195, 10.0.0.2",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "203.0.113.195",
		},
		{
			name:           "Case 3c: Multi-hop where all proxies are trusted returns leftmost",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "10.0.0.3, 10.0.0.2",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "10.0.0.3",
		},
		// Case 4: IPv6 address handling
		{
			name:           "Case 4a: IPv6 remote addr without trusted proxies",
			remoteAddr:     "[2001:db8::1]:8080",
			xff:            "10.0.0.1",
			trustedProxies: nil,
			expectedIP:     "2001:db8::1",
		},
		{
			name:           "Case 4b: IPv6 remote addr bare (no port)",
			remoteAddr:     "::1",
			xff:            "10.0.0.1",
			trustedProxies: nil,
			expectedIP:     "::1",
		},
		{
			name:           "Case 4c: IPv6 trusted proxy forward IPv6 client",
			remoteAddr:     "[::1]:12345",
			xff:            "2001:db8::cafe",
			trustedProxies: []string{"::1"},
			expectedIP:     "2001:db8::cafe",
		},
		{
			name:           "Case 4d: IPv6 CIDR trusted proxy forwarding client",
			remoteAddr:     "[fd00::1]:12345",
			xff:            "203.0.113.50",
			trustedProxies: []string{"fd00::/8"},
			expectedIP:     "203.0.113.50",
		},
		// Case 5: Empty values and invalid IP formats
		{
			name:           "Case 5a: Empty RemoteAddr",
			remoteAddr:     "",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "",
		},
		{
			name:           "Case 5b: Invalid RemoteAddr",
			remoteAddr:     "invalid-ip:80",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "",
		},
		{
			name:           "Case 5c: Invalid IP in XFF falls back to RemoteAddr",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "not-an-ip",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "10.0.0.1",
		},
		{
			name:           "Case 5d: Trusted proxy with X-Real-IP fallback",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "",
			xRealIP:        "203.0.113.199",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "203.0.113.199",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req, _ := http.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}
			c.Request = req

			got := netutil.ClientIP(c, tt.trustedProxies)
			if got != tt.expectedIP {
				t.Errorf("netutil.ClientIP() = %q, want %q", got, tt.expectedIP)
			}
		})
	}
}

func TestBuildEngine_TrustedProxiesConfiguration(t *testing.T) {
	// 1. Without trusted proxies: SetTrustedProxies(nil) and ForwardedByClientIP = false
	cfgNoProxy := &config.Config{
		Server: config.ServerConfig{
			TrustedProxies: nil,
		},
	}
	engineNoProxy := buildEngineWithConfig(cfgNoProxy)
	if engineNoProxy.ForwardedByClientIP {
		t.Errorf("expected ForwardedByClientIP to be false when trusted_proxies is empty")
	}

	// 2. With trusted proxies: SetTrustedProxies populated
	cfgWithProxy := &config.Config{
		Server: config.ServerConfig{
			TrustedProxies: []string{"10.0.0.0/8", "127.0.0.1"},
		},
	}
	engineWithProxy := buildEngineWithConfig(cfgWithProxy)
	if !engineWithProxy.ForwardedByClientIP {
		t.Errorf("expected ForwardedByClientIP to remain true when trusted_proxies is configured")
	}
}

func TestIPWhitelist_Integration_SpoofedXFFRejected(t *testing.T) {
	// Setup route with IP Whitelist plugin
	engine := gin.New()
	engine.Use(gin.Recovery())

	// Server has NO trusted proxies
	_ = engine.SetTrustedProxies(nil)
	engine.ForwardedByClientIP = false
	netutil.SetDefaultTrustedProxies(nil)

	// Create IP whitelist plugin allowing 10.1.2.3 only
	p := ipwhitelist.New()
	if err := p.Init(map[string]interface{}{
		"ip_whitelist": []interface{}{"10.1.2.3"},
	}); err != nil {
		t.Fatalf("failed to init ip whitelist plugin: %v", err)
	}

	// Register test route protected by ipwhitelist
	engine.GET("/api/admin", func(c *gin.Context) {
		if err := p.Execute(c); err != nil {
			return
		}
		if c.IsAborted() {
			return
		}
		c.JSON(200, gin.H{
			"message":   "welcome",
			"client_ip": netutil.ClientIP(c, nil),
		})
	})

	// 1. Client connects from untrusted 192.168.1.50, spoofing XFF: 10.1.2.3
	req, _ := http.NewRequest("GET", "/api/admin", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	req.Header.Set("X-Forwarded-For", "10.1.2.3")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	// MUST be rejected with 403 Forbidden!
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for spoofed XFF without trusted proxies, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	msg, _ := resp["message"].(string)
	if msg == "" {
		msg, _ = resp["error"].(string)
	}
	if msg != "IP 不在白名单中" {
		t.Fatalf("expected message 'IP 不在白名单中', got: %v", msg)
	}

	// 2. Client connects from whitelisted IP directly (10.1.2.3)
	reqValid, _ := http.NewRequest("GET", "/api/admin", nil)
	reqValid.RemoteAddr = "10.1.2.3:12345"

	wValid := httptest.NewRecorder()
	engine.ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid direct client IP, got %d", wValid.Code)
	}
}

func TestIPWhitelist_Integration_TrustedProxyAllowed(t *testing.T) {
	// Configure server with trusted proxy 10.0.0.1
	engine := gin.New()
	engine.Use(gin.Recovery())
	_ = engine.SetTrustedProxies([]string{"10.0.0.1"})
	netutil.SetDefaultTrustedProxies([]string{"10.0.0.1"})
	defer netutil.SetDefaultTrustedProxies(nil)

	p := ipwhitelist.New()
	if err := p.Init(map[string]interface{}{
		"ip_whitelist": []interface{}{"203.0.113.195"},
	}); err != nil {
		t.Fatalf("failed to init ip whitelist plugin: %v", err)
	}

	engine.GET("/api/secure", func(c *gin.Context) {
		if err := p.Execute(c); err != nil {
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Trusted LB at 10.0.0.1 forwards allowed client 203.0.113.195
	req, _ := http.NewRequest("GET", "/api/secure", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK via trusted proxy, got %d (body: %s)", w.Code, w.Body.String())
	}
}
