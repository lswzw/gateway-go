package config

import (
	"testing"
	"time"
)

func TestValidateServerConfigTrustedProxies(t *testing.T) {
	cfg := &ServerConfig{
		Port:                    8080,
		ReadTimeout:             5 * time.Second,
		WriteTimeout:            5 * time.Second,
		MaxHeaderBytes:          1024,
		GracefulShutdownTimeout: 5 * time.Second,
		TrustedProxies:          []string{"127.0.0.1", "10.0.0.0/8", "::1"},
	}
	if err := validateServerConfig(cfg); err != nil {
		t.Fatalf("expected valid server config, got: %v", err)
	}

	invalidCfg := *cfg
	invalidCfg.TrustedProxies = []string{"not-an-ip"}
	if err := validateServerConfig(&invalidCfg); err == nil {
		t.Fatalf("expected error for invalid IP in trusted_proxies")
	}

	invalidCIDR := *cfg
	invalidCIDR.TrustedProxies = []string{"10.0.0.0/99"}
	if err := validateServerConfig(&invalidCIDR); err == nil {
		t.Fatalf("expected error for invalid CIDR in trusted_proxies")
	}
}

func TestValidateLogConfigBodyLimit(t *testing.T) {
	cfg := &LogConfig{
		Level:      "info",
		Format:     "json",
		Output:     "stdout",
		MaxSize:    100,
		MaxAge:     30,
		MaxBackups: 10,
		BodyLimit:  4096,
	}
	if err := validateLogConfig(cfg); err != nil {
		t.Fatalf("expected valid log config, got: %v", err)
	}

	negCfg := *cfg
	negCfg.BodyLimit = -1
	if err := validateLogConfig(&negCfg); err == nil {
		t.Fatalf("expected error for negative body_limit")
	}

	tooLargeCfg := *cfg
	tooLargeCfg.BodyLimit = 70000
	if err := validateLogConfig(&tooLargeCfg); err == nil {
		t.Fatalf("expected error for body_limit > 65536")
	}
}

func TestValidateRouteConfigTargetTimeoutAndRetries(t *testing.T) {
	route := RouteConfig{
		Name: "test-route",
		Match: RouteMatch{
			Type: "prefix",
			Path: "/test",
		},
		Target: TargetConfig{
			URL:     "http://localhost:8080",
			Timeout: 1000,
			Retries: 2,
		},
	}
	if err := validateRouteConfig(&route); err != nil {
		t.Fatalf("expected valid route config, got: %v", err)
	}

	invalidTimeout := route
	invalidTimeout.Target.Timeout = -1
	if err := validateRouteConfig(&invalidTimeout); err == nil {
		t.Fatal("expected error for negative target.timeout")
	}

	invalidRetries := route
	invalidRetries.Target.Retries = -1
	if err := validateRouteConfig(&invalidRetries); err == nil {
		t.Fatal("expected error for negative target.retries")
	}
}
