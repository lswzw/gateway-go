package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"
	"gateway-go/internal/response"
)

// closeNotifyRecorder 包装 httptest.ResponseRecorder 并实现 http.CloseNotifier，
// 防止 httputil.ReverseProxy 在测试环境下触发 gin.ResponseWriter.CloseNotify 类型转换 panic
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closed:           make(chan bool, 1),
	}
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return r.closed
}

// verifyUnifiedErrorResponse 校验错误响应是否为合法的统一错误 JSON，且写入刚好一次（无多余数据）
func verifyUnifiedErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, expectedStatus int, expectedReqID string) response.ErrorResponse {
	t.Helper()

	if rec.Code != expectedStatus {
		t.Fatalf("期望状态码 %d，实际获得 %d，body: %s", expectedStatus, rec.Code, rec.Body.String())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("期望 Content-Type 为 application/json，实际获得: %s", ct)
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得: %s", expectedCL, cl)
	}

	body := rec.Body.Bytes()
	var errResp response.ErrorResponse
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&errResp); err != nil {
		t.Fatalf("解析响应体为 JSON 失败: %v, raw body: %s", err, string(body))
	}

	if dec.More() {
		t.Fatalf("响应体包含多个 JSON 对象或拼接数据，未满足写入刚好一次: raw body: %s", string(body))
	}

	if errResp.Code != expectedStatus {
		t.Errorf("期望 JSON 内 code 为 %d，实际获得 %d", expectedStatus, errResp.Code)
	}

	if expectedReqID != "" && errResp.RequestID != expectedReqID {
		t.Errorf("期望 JSON 内 request_id 为 %s，实际获得 %s", expectedReqID, errResp.RequestID)
	}

	if errResp.Message == "" {
		t.Errorf("期望 JSON 内 message 不为空")
	}

	return errResp
}

func TestGatewayErrorHandlingWithoutErrorPlugin(t *testing.T) {
	// Case 1: 正常路由转发至上游并在无 error 插件下返回 200
	t.Run("Case1_NormalRouteProxiesToUpstream200", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"upstream_status":"healthy","message":"hello from upstream"}`))
		}))
		defer upstream.Close()

		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		cfg := &config.Config{
			Routes: []config.RouteConfig{
				{
					Name: "normal-service-route",
					Match: config.RouteMatch{
						Path: "/api/normal",
						Type: "prefix",
					},
					Target: config.TargetConfig{
						URL: upstream.URL + "/api/normal",
					},
				},
			},
		}
		configManager.SetConfig(cfg)

		engine := buildEngine()

		req, err := http.NewRequest("GET", "/api/normal/ping", nil)
		if err != nil {
			t.Fatalf("创建请求失败: %v", err)
		}
		req.Header.Set("X-Request-Id", "req-normal-01")
		rec := newCloseNotifyRecorder()

		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("期望状态码 200，实际获得: %d, body: %s", rec.Code, rec.Body.String())
		}

		if !strings.Contains(rec.Body.String(), "upstream_status") {
			t.Errorf("期望响应体包含上游数据，实际获得: %s", rec.Body.String())
		}
	})

	// Case 2: 代理失败 502 / 上游错误：网关兜底输出统一 JSON {code, message, request_id} 且写入刚好一次
	t.Run("Case2_ProxyFailure502AndInternal500OutputsUnifiedJSON", func(t *testing.T) {
		// 创建一个已关闭的上游服务以模拟代理连接拒绝（502 Bad Gateway）
		closedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		closedURL := closedUpstream.URL
		closedUpstream.Close() // 立即关闭以造成连接失败

		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		// 注册一个仅返回错误且未写响应的测试插件以模拟网关内部错误（500）
		p500 := newMockErrorOnlyPlugin("failing_plugin", fmt.Errorf("plugin internal database error"))
		_ = pluginManager.Register(p500)
		_ = pluginManager.RegisterAvailablePlugin("failing_plugin", p500)
		_ = pluginManager.LoadRoutePlugins("internal-500-route", []string{"failing_plugin"})

		cfg := &config.Config{
			Routes: []config.RouteConfig{
				{
					Name: "proxy-failure-route",
					Match: config.RouteMatch{
						Path: "/api/unreachable",
						Type: "prefix",
					},
					Target: config.TargetConfig{
						URL: closedURL,
					},
				},
				{
					Name: "internal-500-route",
					Match: config.RouteMatch{
						Path: "/api/internal-error",
						Type: "prefix",
					},
					Target: config.TargetConfig{
						URL: "internal://mock",
					},
					Plugins: []string{"failing_plugin"},
				},
			},
		}
		configManager.SetConfig(cfg)

		engine := buildEngine()

		// 场景 2a: 代理失败返回 502 Bad Gateway 统一 JSON
		{
			req, err := http.NewRequest("GET", "/api/unreachable", nil)
			if err != nil {
				t.Fatalf("创建请求失败: %v", err)
			}
			req.Header.Set("X-Request-Id", "req-proxy-fail-502")
			rec := newCloseNotifyRecorder()

			engine.ServeHTTP(rec, req)

			errResp := verifyUnifiedErrorResponse(t, rec.ResponseRecorder, http.StatusBadGateway, "req-proxy-fail-502")
			if !strings.Contains(errResp.Message, "代理请求失败") {
				t.Errorf("期望 502 错误消息包含 '代理请求失败'，实际获得: %s", errResp.Message)
			}
		}

		// 场景 2b: 网关内部处理失败返回 500 Internal Server Error 统一 JSON
		{
			req, err := http.NewRequest("GET", "/api/internal-error", nil)
			if err != nil {
				t.Fatalf("创建请求失败: %v", err)
			}
			req.Header.Set("X-Request-Id", "req-internal-500")
			rec := httptest.NewRecorder()

			engine.ServeHTTP(rec, req)

			errResp := verifyUnifiedErrorResponse(t, rec, http.StatusInternalServerError, "req-internal-500")
			if !strings.Contains(errResp.Message, "plugin internal database error") {
				t.Errorf("期望 500 错误消息包含插件错误，实际获得: %s", errResp.Message)
			}
		}
	})

	// Case 3: 未知路由返回 404 并携带统一 JSON 格式 {code, message, request_id}
	t.Run("Case3_UnknownRouteReturns404UnifiedJSON", func(t *testing.T) {
		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		cfg := &config.Config{
			Routes: []config.RouteConfig{
				{
					Name: "existing-route",
					Match: config.RouteMatch{
						Path: "/api/exists",
						Type: "exact",
					},
					Target: config.TargetConfig{
						URL: "internal://exists",
					},
				},
			},
		}
		configManager.SetConfig(cfg)

		engine := buildEngine()

		req, err := http.NewRequest("GET", "/api/non-existent-route-xyz", nil)
		if err != nil {
			t.Fatalf("创建请求失败: %v", err)
		}
		req.Header.Set("X-Request-Id", "req-unknown-404")
		rec := httptest.NewRecorder()

		engine.ServeHTTP(rec, req)

		errResp := verifyUnifiedErrorResponse(t, rec, http.StatusNotFound, "req-unknown-404")
		if errResp.Message != "未找到匹配的路由" {
			t.Errorf("期望 404 错误消息为 '未找到匹配的路由'，实际获得: %s", errResp.Message)
		}
	})

	// Case 4: 高并发压测：-race 下 1000 次请求无 panic 与数据竞争
	t.Run("Case4_ConcurrentStress1000RequestsNoRaceNoPanic", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer upstream.Close()

		closedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		closedURL := closedUpstream.URL
		closedUpstream.Close()

		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		cfg := &config.Config{
			Routes: []config.RouteConfig{
				{
					Name: "stress-normal-route",
					Match: config.RouteMatch{
						Path: "/api/stress/normal",
						Type: "prefix",
					},
					Target: config.TargetConfig{
						URL: upstream.URL + "/api/stress/normal",
					},
				},
				{
					Name: "stress-failing-route",
					Match: config.RouteMatch{
						Path: "/api/stress/failing",
						Type: "prefix",
					},
					Target: config.TargetConfig{
						URL: closedURL,
					},
				},
			},
		}
		configManager.SetConfig(cfg)

		engine := buildEngine()

		const totalRequests = 1000
		const concurrency = 25
		const requestsPerWorker = totalRequests / concurrency

		var successCount int64
		var proxyFailCount int64
		var notFoundCount int64

		var wg sync.WaitGroup
		wg.Add(concurrency)

		for worker := 0; worker < concurrency; worker++ {
			go func(wID int) {
				defer wg.Done()
				for i := 0; i < requestsPerWorker; i++ {
					reqIdx := wID*requestsPerWorker + i
					reqID := fmt.Sprintf("req-stress-%d", reqIdx)

					var targetPath string
					switch reqIdx % 3 {
					case 0:
						targetPath = "/api/stress/normal"
					case 1:
						targetPath = "/api/stress/failing"
					case 2:
						targetPath = "/api/stress/unknown-route"
					}

					req, err := http.NewRequest("GET", targetPath, nil)
					if err != nil {
						t.Errorf("创建请求失败: %v", err)
						return
					}
					req.Header.Set("X-Request-Id", reqID)
					rec := newCloseNotifyRecorder()

					// 执行请求
					engine.ServeHTTP(rec, req)

					switch reqIdx % 3 {
					case 0:
						if rec.Code != http.StatusOK {
							t.Errorf("请求 %d 期望状态码 200，实际获得: %d", reqIdx, rec.Code)
						}
						atomic.AddInt64(&successCount, 1)
					case 1:
						verifyUnifiedErrorResponse(t, rec.ResponseRecorder, http.StatusBadGateway, reqID)
						atomic.AddInt64(&proxyFailCount, 1)
					case 2:
						verifyUnifiedErrorResponse(t, rec.ResponseRecorder, http.StatusNotFound, reqID)
						atomic.AddInt64(&notFoundCount, 1)
					}
				}
			}(worker)
		}

		wg.Wait()

		if total := successCount + proxyFailCount + notFoundCount; total != totalRequests {
			t.Fatalf("期望完成 %d 次请求，实际完成 %d 次 (200: %d, 502: %d, 404: %d)",
				totalRequests, total, successCount, proxyFailCount, notFoundCount)
		}
	})

	// Case 5: 加载包含已弃用 error 插件的配置时返回明确的废弃错误
	t.Run("Case5_LoadingConfigWithErrorPluginReturnsDeprecationError", func(t *testing.T) {
		mgr := plugin.NewManager()
		expectedErrMsg := "error plugin has been deprecated; error handling is now unified into gateway core respondError"

		// 5a. LoadAvailablePlugins 启用 error 插件配置
		errEnabled := mgr.LoadAvailablePlugins([]plugin.PluginConfig{
			{
				Name:    "error",
				Enabled: true,
				Order:   100,
			},
		})
		if errEnabled == nil {
			t.Fatalf("期望加载启用的 error 插件时报错，但返回 nil")
		}
		if !strings.Contains(errEnabled.Error(), expectedErrMsg) {
			t.Errorf("期望错误信息包含 %q，实际获得: %s", expectedErrMsg, errEnabled.Error())
		}

		// 5b. LoadAvailablePlugins 未启用 error 插件配置（同样应提示已下线）
		errDisabled := mgr.LoadAvailablePlugins([]plugin.PluginConfig{
			{
				Name:    "error",
				Enabled: false,
				Order:   100,
			},
		})
		if errDisabled == nil {
			t.Fatalf("期望加载未启用的 error 插件时报错，但返回 nil")
		}
		if !strings.Contains(errDisabled.Error(), expectedErrMsg) {
			t.Errorf("期望错误信息包含 %q，实际获得: %s", expectedErrMsg, errDisabled.Error())
		}

		// 5c. LoadRoutePlugins 配置了 error 插件
		errRoute := mgr.LoadRoutePlugins("test-route", []string{"error"})
		if errRoute == nil {
			t.Fatalf("期望路由加载 error 插件时报错，但返回 nil")
		}
		if !strings.Contains(errRoute.Error(), expectedErrMsg) {
			t.Errorf("期望错误信息包含 %q，实际获得: %s", expectedErrMsg, errRoute.Error())
		}

		// 5d. 配置校验器 ValidateConfig 检测到 error 插件
		cfgWithError := &config.Config{
			Server: config.ServerConfig{
				Port:                    8080,
				ReadTimeout:             10 * time.Second,
				WriteTimeout:            10 * time.Second,
				MaxHeaderBytes:          1048576,
				GracefulShutdownTimeout: 10 * time.Second,
			},
			Log: config.LogConfig{
				Level:      "info",
				Format:     "json",
				Output:     "stdout",
				MaxSize:    100,
				MaxAge:     30,
				MaxBackups: 10,
				BodyLimit:  4096,
			},
			Plugins: config.PluginsConfig{
				Available: []config.PluginConfig{
					{
						Name:    "error",
						Enabled: true,
						Order:   100,
					},
				},
			},
			Routes: []config.RouteConfig{
				{
					Name: "dummy",
					Match: config.RouteMatch{
						Path: "/api/dummy",
					},
					Target: config.TargetConfig{
						URL: "internal://dummy",
					},
				},
			},
		}
		errValidate := config.ValidateConfig(cfgWithError)
		if errValidate == nil {
			t.Fatalf("期望 ValidateConfig 检测到 error 插件报错，但返回 nil")
		}
		if !strings.Contains(errValidate.Error(), expectedErrMsg) {
			t.Errorf("期望 ValidateConfig 错误信息包含 %q，实际获得: %s", expectedErrMsg, errValidate.Error())
		}
	})
}
