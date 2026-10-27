package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/metrics"
	"gateway-go/internal/plugin"
	"gateway-go/internal/response"
)

// TestAdminServerEndpoints 验证 Admin 端口各端点行为 (REQ-P1-04)
func TestAdminServerEndpoints(t *testing.T) {
	// 重置并记录测试指标
	metrics.Reset()
	metrics.RecordRequest("test_route", "GET", "200")
	metrics.RecordUpstreamLatency("test_route", 25*time.Millisecond)
	metrics.RecordRateLimitRejected("test_route")
	metrics.RecordCircuitOpen("test_route")
	metrics.RecordReload("success")
	metrics.SetConfigVersion("1.0.0-test")

	var readyFlag int32 = 1
	handler := newAdminHandler(func() bool {
		return atomic.LoadInt32(&readyFlag) == 1
	})
	adminServer := httptest.NewServer(handler)
	defer adminServer.Close()

	client := adminServer.Client()

	// 1. /healthz 存活探针测试
	t.Run("HealthzReturns200", func(t *testing.T) {
		resp, err := client.Get(adminServer.URL + "/healthz")
		if err != nil {
			t.Fatalf("GET /healthz failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var result map[string]string
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v, body: %s", err, string(body))
		}
		if result["status"] != "ok" {
			t.Fatalf("expected status 'ok', got %q", result["status"])
		}
	})

	// 2. /readyz 就绪探针 (Ready) 测试
	t.Run("ReadyzReturns200WhenReady", func(t *testing.T) {
		atomic.StoreInt32(&readyFlag, 1)
		resp, err := client.Get(adminServer.URL + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var result map[string]string
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v, body: %s", err, string(body))
		}
		if result["status"] != "ready" {
			t.Fatalf("expected status 'ready', got %q", result["status"])
		}
	})

	// 3. /readyz 就绪探针 (Unready) 测试
	t.Run("ReadyzReturns503WhenUnready", func(t *testing.T) {
		atomic.StoreInt32(&readyFlag, 0)
		resp, err := client.Get(adminServer.URL + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected status 503, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var result map[string]string
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v, body: %s", err, string(body))
		}
		if result["status"] != "unready" {
			t.Fatalf("expected status 'unready', got %q", result["status"])
		}
	})

	// 4. /metrics Prometheus 监控指标端点测试
	t.Run("MetricsReturns200AndPrometheusMetrics", func(t *testing.T) {
		resp, err := client.Get(adminServer.URL + "/metrics")
		if err != nil {
			t.Fatalf("GET /metrics failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read /metrics body: %v", err)
		}
		metricsText := string(bodyBytes)

		expectedMetrics := []string{
			"gateway_requests_total",
			"gateway_upstream_rt_seconds",
			"gateway_ratelimit_rejected_total",
			"gateway_circuit_open_total",
			"gateway_reload_total",
			"gateway_config_version",
			"go_goroutines",
		}

		for _, em := range expectedMetrics {
			if !strings.Contains(metricsText, em) {
				t.Errorf("metrics output missing expected metric %q", em)
			}
		}
	})
}

// TestReadyzUnhealthy 独立验证就绪检查未就绪时的状态码 (REQ-P1-04)
func TestReadyzUnhealthy(t *testing.T) {
	handler := newAdminHandler(func() bool {
		return false
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "unready") {
		t.Fatalf("expected body to contain 'unready', got %s", string(body))
	}
}

// TestBusinessPortNoAdminLeaks 验证业务端口严格禁止泄露任何管理/探针端点 (REQ-P1-04)
func TestBusinessPortNoAdminLeaks(t *testing.T) {
	// 构建带有默认路由及自定义业务路由的配置
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port: 8080,
		},
		Routes: []config.RouteConfig{
			{
				Name: "catch-all",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/",
					Priority: 1,
				},
				Target: config.TargetConfig{
					URL: "internal://default",
				},
				Response: &config.ResponseConfig{
					Status:  200,
					Content: "business-traffic-only",
				},
			},
		},
	}

	engine := buildEngineWithConfig(cfg)

	adminPaths := []string{
		"/metrics",
		"/healthz",
		"/readyz",
		"/gatewaygo/health",
	}

	for _, path := range adminPaths {
		t.Run("Path_"+strings.TrimPrefix(path, "/"), func(t *testing.T) {
			req, _ := http.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404 for admin path %s on business port, got %d", path, w.Code)
			}

			// 验证响应为统一错误结构并包含 X-Request-ID
			reqID := w.Header().Get("X-Request-ID")
			if reqID == "" {
				t.Errorf("expected X-Request-ID header on 404 response for %s", path)
			}

			var errResp response.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("failed to parse JSON response: %v, body: %s", err, w.Body.String())
			}
			if errResp.Code != http.StatusNotFound {
				t.Errorf("expected errResp.Code 404, got %d", errResp.Code)
			}
		})
	}
}

// TestRequestIDPropagation 验证 Request-ID 在全链路的生成、透传与回写 (REQ-P1-04)
func TestRequestIDPropagation(t *testing.T) {
	var capturedUpstreamReqID string
	var upstreamCalled int32

	// 创建假上游服务器，检查接收到的 X-Request-ID
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalled, 1)
		capturedUpstreamReqID = r.Header.Get("X-Request-ID")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-response"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port: 8080,
		},
		Routes: []config.RouteConfig{
			{
				Name: "test-proxy-route",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api",
					Priority: 10,
				},
				Target: config.TargetConfig{
					URL: upstream.URL,
				},
			},
		},
	}

	pm := plugin.NewManager()
	pluginManager = pm
	engine := buildEngineWithConfig(cfg)
	gwServer := httptest.NewServer(engine)
	defer gwServer.Close()

	client := gwServer.Client()

	// Case 1: 客户端未提供 X-Request-ID，网关自动生成 UUID
	t.Run("AutoGeneratedRequestID", func(t *testing.T) {
		capturedUpstreamReqID = ""
		req, _ := http.NewRequest("GET", gwServer.URL+"/api/user", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d, body: %s", resp.StatusCode, string(body))
		}

		clientRespReqID := resp.Header.Get("X-Request-ID")
		if clientRespReqID == "" {
			t.Fatal("expected X-Request-ID in client response header, got empty")
		}

		// 上游必须收到 X-Request-ID 且与客户端收到的完全一致
		if capturedUpstreamReqID == "" {
			t.Fatal("upstream did not receive X-Request-ID header")
		}
		if clientRespReqID != capturedUpstreamReqID {
			t.Fatalf("mismatch: client got %q, upstream got %q", clientRespReqID, capturedUpstreamReqID)
		}

		// 格式符合 UUID (36 字符包含 4 个连字符)
		if len(clientRespReqID) != 36 || strings.Count(clientRespReqID, "-") != 4 {
			t.Errorf("expected UUID format, got %q", clientRespReqID)
		}
	})

	// Case 2: 客户端提供标准 X-Request-ID，网关原样保留并透传
	t.Run("PreserveCustomRequestID", func(t *testing.T) {
		capturedUpstreamReqID = ""
		customID := "trace-custom-id-987654321"
		req, _ := http.NewRequest("GET", gwServer.URL+"/api/order", nil)
		req.Header.Set("X-Request-ID", customID)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		clientRespReqID := resp.Header.Get("X-Request-ID")
		if clientRespReqID != customID {
			t.Fatalf("expected client response X-Request-ID %q, got %q", customID, clientRespReqID)
		}
		if capturedUpstreamReqID != customID {
			t.Fatalf("expected upstream received X-Request-ID %q, got %q", customID, capturedUpstreamReqID)
		}
	})

	// Case 3: 客户端提供小写 x-request-id，网关原样保留并透传
	t.Run("PreserveLowercaseRequestID", func(t *testing.T) {
		capturedUpstreamReqID = ""
		customID := "trace-lower-id-123456"
		req, _ := http.NewRequest("GET", gwServer.URL+"/api/order", nil)
		req.Header.Set("x-request-id", customID)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		clientRespReqID := resp.Header.Get("X-Request-ID")
		if clientRespReqID != customID {
			t.Fatalf("expected client response X-Request-ID %q, got %q", customID, clientRespReqID)
		}
		if capturedUpstreamReqID != customID {
			t.Fatalf("expected upstream received X-Request-ID %q, got %q", customID, capturedUpstreamReqID)
		}
	})
}

// TestAdminServerDualPortLifecycle 验证业务端口与管理端口的双端口物理分离与生命周期优雅退出
func TestAdminServerDualPortLifecycle(t *testing.T) {
	businessPort := getFreePort(t)
	adminPort := getFreePort(t)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    businessPort,
			GracefulShutdownTimeout: 2 * time.Second,
		},
		Admin: config.AdminConfig{
			Listen: fmt.Sprintf("127.0.0.1:%d", adminPort),
		},
	}

	pm := plugin.NewManager()
	engine := buildEngineWithConfig(cfg)
	srv := newHTTPServer(cfg, engine)
	adminSrv := newAdminHTTPServer(cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- runServerWithAdmin(ctx, srv, adminSrv, cfg, pm, nil)
	}()

	waitForServerReady(t, businessPort)
	waitForServerReady(t, adminPort)

	// 验证业务端口可正常访问业务路由（此处未匹配路由返回 404）
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", businessPort))
	if err != nil {
		t.Fatalf("business port get failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("business port should return 404 for /metrics, got %d", resp.StatusCode)
	}

	// 验证管理端口可正常返回 /healthz
	respAdmin, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", adminPort))
	if err != nil {
		t.Fatalf("admin port get failed: %v", err)
	}
	respAdmin.Body.Close()
	if respAdmin.StatusCode != http.StatusOK {
		t.Fatalf("admin port should return 200 for /healthz, got %d", respAdmin.StatusCode)
	}

	// 触发上下文退出
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServerWithAdmin exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shutdown within 5s")
	}
}
