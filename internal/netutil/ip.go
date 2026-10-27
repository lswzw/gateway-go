package netutil

import (
	"net"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var (
	defaultMu             sync.RWMutex
	defaultTrustedProxies []string
)

// SetDefaultTrustedProxies sets the package-level default trusted proxies.
func SetDefaultTrustedProxies(proxies []string) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if proxies == nil {
		defaultTrustedProxies = nil
		return
	}
	defaultTrustedProxies = make([]string, len(proxies))
	copy(defaultTrustedProxies, proxies)
}

// GetDefaultTrustedProxies returns a copy of the default trusted proxies.
func GetDefaultTrustedProxies() []string {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	if defaultTrustedProxies == nil {
		return nil
	}
	res := make([]string, len(defaultTrustedProxies))
	copy(res, defaultTrustedProxies)
	return res
}

// GetClientIP returns the trusted client IP using the default trusted proxies.
func GetClientIP(c *gin.Context) string {
	return ClientIP(c, GetDefaultTrustedProxies())
}

// CIDRMatcher checks whether an IP belongs to any of the configured CIDRs.
type CIDRMatcher struct {
	nets []*net.IPNet
}

// NewCIDRMatcher creates a new CIDRMatcher from a list of IP/CIDR strings.
func NewCIDRMatcher(trustedProxies []string) *CIDRMatcher {
	matcher := &CIDRMatcher{
		nets: make([]*net.IPNet, 0, len(trustedProxies)),
	}
	for _, proxy := range trustedProxies {
		trimmed := strings.TrimSpace(proxy)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "/") {
			ip := ParseIP(trimmed)
			if ip == nil {
				continue
			}
			if ip.To4() != nil {
				trimmed += "/32"
			} else {
				trimmed += "/128"
			}
		}
		_, ipNet, err := net.ParseCIDR(trimmed)
		if err == nil && ipNet != nil {
			matcher.nets = append(matcher.nets, ipNet)
		}
	}
	return matcher
}

// Contains checks if the IP is covered by any trusted CIDR.
func (m *CIDRMatcher) Contains(ip net.IP) bool {
	if m == nil || len(m.nets) == 0 || ip == nil {
		return false
	}
	for _, n := range m.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseIP parses a raw string representing an IP address or host:port into net.IP.
// It handles IPv4, IPv6 (with or without brackets), and port stripping.
func ParseIP(raw string) net.IP {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}

	host, _, err := net.SplitHostPort(s)
	if err != nil {
		host = strings.Trim(s, "[]")
	} else {
		host = strings.Trim(host, "[]")
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip
}

// ClientIP resolves the trusted client IP for the given request context.
//
// 1. If trustedProxies is empty:
//    The gateway is directly exposed to clients. It NEVER trusts X-Forwarded-For
//    or X-Real-IP headers. The client IP is extracted directly from c.Request.RemoteAddr.
//
// 2. If trustedProxies is not empty:
//    The direct peer (RemoteAddr) must be in trustedProxies. If not, RemoteAddr is returned.
//    If RemoteAddr is trusted, X-Forwarded-For is evaluated from right to left,
//    extracting the rightmost untrusted IP address.
//    If X-Forwarded-For is missing or invalid, X-Real-IP is used as a fallback.
//    If neither header yields a valid IP, RemoteAddr is returned.
func ClientIP(c *gin.Context, trustedProxies []string) string {
	if c == nil || c.Request == nil {
		return ""
	}

	remoteIP := ParseIP(c.Request.RemoteAddr)
	if remoteIP == nil {
		return ""
	}

	// Case 1: len(trustedProxies) == 0 -> Directly exposed, never trust forwarded headers.
	if len(trustedProxies) == 0 {
		return remoteIP.String()
	}

	matcher := NewCIDRMatcher(trustedProxies)

	// If the immediate peer (RemoteAddr) is not a trusted proxy, do not trust forwarded headers.
	if !matcher.Contains(remoteIP) {
		return remoteIP.String()
	}

	// 1. Try X-Forwarded-For
	if xff := c.Request.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := parseXForwardedFor(xff, matcher); ip != "" {
			return ip
		}
	}

	// 2. Try X-Real-IP
	if xri := c.Request.Header.Get("X-Real-IP"); xri != "" {
		if rip := ParseIP(xri); rip != nil {
			return rip.String()
		}
	}

	return remoteIP.String()
}

// parseXForwardedFor parses a multi-hop X-Forwarded-For header and extracts
// the rightmost untrusted IP address.
func parseXForwardedFor(header string, matcher *CIDRMatcher) string {
	items := strings.Split(header, ",")
	for i := len(items) - 1; i >= 0; i-- {
		item := strings.TrimSpace(items[i])
		if item == "" {
			continue
		}

		ip := ParseIP(item)
		if ip == nil {
			// Intermediate proxy sent an invalid IP; stop parsing per security convention.
			break
		}

		// Stop and return when an untrusted IP is encountered,
		// or when the leftmost IP (the original client) is reached.
		if i == 0 || !matcher.Contains(ip) {
			return ip.String()
		}
	}
	return ""
}
