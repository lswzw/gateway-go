package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
		// Case 1: No trusted proxies configured + spoofed XFF -> returns RemoteAddr (spoofed IP ignored!)
		{
			name:           "Case 1: No trusted proxies configured + spoofed XFF",
			remoteAddr:     "192.168.1.50:54321",
			xff:            "10.1.2.3",
			trustedProxies: nil,
			expectedIP:     "192.168.1.50",
		},
		{
			name:           "Case 1b: Empty slice trusted proxies + spoofed XFF and X-Real-IP",
			remoteAddr:     "192.168.1.50:1234",
			xff:            "10.1.2.3",
			xRealIP:        "10.1.2.4",
			trustedProxies: []string{},
			expectedIP:     "192.168.1.50",
		},
		// Case 2: Single trusted proxy + XFF -> returns client IP
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
			name:           "Case 2c: Untrusted remote addr sending XFF ignored even if trusted proxies configured",
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
		{
			name:           "Case 4e: IPv6 bracketed with port in XFF",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "[2001:db8::1]:4567, 10.0.0.2",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "2001:db8::1",
		},
		// Case 5: Empty values, fallback to X-Real-IP, and invalid IP formats
		{
			name:           "Case 5a: Empty RemoteAddr",
			remoteAddr:     "",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "",
		},
		{
			name:           "Case 5b: Invalid RemoteAddr",
			remoteAddr:     "not-an-ip:80",
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
			name:           "Case 5d: Trusted proxy with X-Real-IP fallback when XFF empty",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "",
			xRealIP:        "203.0.113.199",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "203.0.113.199",
		},
		{
			name:           "Case 5e: Untrusted proxy with X-Real-IP ignored",
			remoteAddr:     "192.168.1.1:12345",
			xRealIP:        "203.0.113.199",
			trustedProxies: []string{"10.0.0.0/8"},
			expectedIP:     "192.168.1.1",
		},
		{
			name:           "Case 5f: RemoteAddr without port and no trusted proxies",
			remoteAddr:     "192.168.1.100",
			trustedProxies: nil,
			expectedIP:     "192.168.1.100",
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

			got := ClientIP(c, tt.trustedProxies)
			if got != tt.expectedIP {
				t.Errorf("ClientIP() = %q, want %q", got, tt.expectedIP)
			}
		})
	}
}

func TestClientIP_NilContext(t *testing.T) {
	if got := ClientIP(nil, nil); got != "" {
		t.Errorf("expected empty string for nil context, got %q", got)
	}

	c := &gin.Context{Request: nil}
	if got := ClientIP(c, nil); got != "" {
		t.Errorf("expected empty string for nil request, got %q", got)
	}
}

func TestDefaultTrustedProxies(t *testing.T) {
	defer SetDefaultTrustedProxies(nil)

	SetDefaultTrustedProxies([]string{"10.0.0.0/8", "127.0.0.1"})
	defaults := GetDefaultTrustedProxies()
	if len(defaults) != 2 || defaults[0] != "10.0.0.0/8" || defaults[1] != "127.0.0.1" {
		t.Fatalf("unexpected defaults: %v", defaults)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	c.Request = req

	got := GetClientIP(c)
	if got != "203.0.113.1" {
		t.Errorf("GetClientIP() = %q, want %q", got, "203.0.113.1")
	}
}
