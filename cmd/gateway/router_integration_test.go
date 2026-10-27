package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"
	"gateway-go/internal/router"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouterManagerLiveTraffic 集成测试：验证 router.Manager 在网关实际流量中的路由转发行为 (REQ-P1-03)
func TestRouterManagerLiveTraffic(t *testing.T) {
	// 1. 启动三台独立 Mock 上游后端
	backendV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "backend-v1: path=%s, host=%s", r.URL.Path, r.Host)
	}))
	defer backendV1.Close()

	backendV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "backend-v2: path=%s, host=%s", r.URL.Path, r.Host)
	}))
	defer backendV2.Close()

	backendHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "backend-host-specific: path=%s, host=%s", r.URL.Path, r.Host)
	}))
	defer backendHost.Close()

	// 2. 构造测试配置
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port: 8080,
		},
		Log: config.LogConfig{
			Level: "warn",
		},
		Routes: []config.RouteConfig{
			// 路由 1: 前缀 /api/v1 -> backendV1
			{
				Name: "api-v1",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api/v1",
					Priority: 90,
				},
				Target: config.TargetConfig{
					URL: backendV1.URL,
				},
			},
			// 路由 2: 前缀 /api/v2 -> backendV2
			{
				Name: "api-v2",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api/v2",
					Priority: 90,
				},
				Target: config.TargetConfig{
					URL: backendV2.URL,
				},
			},
			// 路由 3: 前缀 /api 带特定 Host v1.api.example.com (priority 80) -> backendHost
			// (核心验收场景：比 generic-api 优先级低，但由于 Host 更具体必须胜出)
			{
				Name: "v1-api-host",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api",
					Host:     "v1.api.example.com",
					Priority: 80,
				},
				Target: config.TargetConfig{
					URL: backendHost.URL,
				},
			},
			// 路由 4: 前缀 /api 无 Host (priority 90) -> backendV1
			{
				Name: "generic-api",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api",
					Priority: 90,
				},
				Target: config.TargetConfig{
					URL: backendV1.URL,
				},
			},
			// 路由 5: 内部响应路由
			{
				Name: "internal-status",
				Match: config.RouteMatch{
					Type:     "exact",
					Path:     "/internal/status",
					Priority: 100,
				},
				Target: config.TargetConfig{
					URL: "internal://default",
				},
				Response: &config.ResponseConfig{
					Status:      200,
					Content:     "gateway-healthy",
					ContentType: "text/plain",
				},
			},
		},
	}

	pm := plugin.NewManager()
	rm := router.NewManagerWithConfig(cfg)
	engine := buildEngineWithConfigPluginsAndRouter(cfg, pm, rm)

	snap := &Snapshot{
		Version:       "v1.0.0",
		Config:        cfg,
		Engine:        engine,
		RouterManager: rm,
		PluginManager: pm,
	}
	dispatcher := NewDispatcher(snap)

	tests := []struct {
		desc           string
		method         string
		urlStr         string
		host           string
		expectCode     int
		expectBodyPart string
	}{
		{
			desc:           "前缀路由正常转发并剥除前缀 (/api/v1/users -> backend-v1)",
			method:         "GET",
			urlStr:         "/api/v1/users",
			expectCode:     http.StatusOK,
			expectBodyPart: "backend-v1: path=/users",
		},
		{
			desc:           "多级子路径正常转发 (/api/v2/items/123/detail -> backend-v2)",
			method:         "GET",
			urlStr:         "/api/v2/items/123/detail",
			expectCode:     http.StatusOK,
			expectBodyPart: "backend-v2: path=/items/123/detail",
		},
		{
			desc:           "REQ-P1-03核心验证：特定Host路由(prio 80)胜过无Host路由(prio 90)",
			method:         "GET",
			urlStr:         "/api/orders",
			host:           "v1.api.example.com",
			expectCode:     http.StatusOK,
			expectBodyPart: "backend-host-specific: path=/orders",
		},
		{
			desc:           "带端口的Host请求依然命中特定Host路由 (v1.api.example.com:8080)",
			method:         "GET",
			urlStr:         "/api/orders",
			host:           "v1.api.example.com:8080",
			expectCode:     http.StatusOK,
			expectBodyPart: "backend-host-specific: path=/orders",
		},
		{
			desc:           "非特定Host请求回退至泛化路由 (other.com -> backend-v1)",
			method:         "GET",
			urlStr:         "/api/orders",
			host:           "other.com",
			expectCode:     http.StatusOK,
			expectBodyPart: "backend-v1: path=/orders",
		},
		{
			desc:           "内部响应路由拦截处理无需上游",
			method:         "GET",
			urlStr:         "/internal/status",
			expectCode:     http.StatusOK,
			expectBodyPart: "gateway-healthy",
		},
		{
			desc:           "未注册路由返回 404 且包含统一错误结构",
			method:         "GET",
			urlStr:         "/unregistered/path",
			expectCode:     http.StatusNotFound,
			expectBodyPart: "未找到匹配的路由",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.urlStr, nil)
			if tt.host != "" {
				req.Host = tt.host
			}
			rec := newCloseNotifyRecorder()

			dispatcher.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tt.expectCode, resp.StatusCode)
			assert.Contains(t, string(body), tt.expectBodyPart)
		})
	}
}

// TestRouterManagerReloadIntegration 集成测试：验证热重载下快照 router.Manager 切换 (REQ-P1-02, REQ-P1-03)
func TestRouterManagerReloadIntegration(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-A"))
	}))
	defer backendA.Close()

	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-B"))
	}))
	defer backendB.Close()

	// 初始快照：仅包含 route-A (/api/a)
	cfg1 := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Routes: []config.RouteConfig{
			{
				Name: "route-a",
				Match: config.RouteMatch{
					Type: "prefix",
					Path: "/api/a",
				},
				Target: config.TargetConfig{URL: backendA.URL},
			},
		},
	}

	pm1 := plugin.NewManager()
	rm1 := router.NewManagerWithConfig(cfg1)
	engine1 := buildEngineWithConfigPluginsAndRouter(cfg1, pm1, rm1)
	snap1 := &Snapshot{
		Version:       "v1",
		Config:        cfg1,
		Engine:        engine1,
		RouterManager: rm1,
		PluginManager: pm1,
	}

	dispatcher := NewDispatcher(snap1)

	// 请求 /api/a -> 200, /api/b -> 404
	reqA := httptest.NewRequest("GET", "/api/a/test", nil)
	recA := newCloseNotifyRecorder()
	dispatcher.ServeHTTP(recA, reqA)
	assert.Equal(t, http.StatusOK, recA.Code)
	assert.Equal(t, "backend-A", recA.Body.String())

	reqB := httptest.NewRequest("GET", "/api/b/test", nil)
	recB := newCloseNotifyRecorder()
	dispatcher.ServeHTTP(recB, reqB)
	assert.Equal(t, http.StatusNotFound, recB.Code)

	// 新快照：增加 route-B (/api/b)
	cfg2 := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Routes: []config.RouteConfig{
			{
				Name: "route-a",
				Match: config.RouteMatch{
					Type: "prefix",
					Path: "/api/a",
				},
				Target: config.TargetConfig{URL: backendA.URL},
			},
			{
				Name: "route-b",
				Match: config.RouteMatch{
					Type: "prefix",
					Path: "/api/b",
				},
				Target: config.TargetConfig{URL: backendB.URL},
			},
		},
	}

	pm2 := plugin.NewManager()
	rm2 := router.NewManagerWithConfig(cfg2)
	engine2 := buildEngineWithConfigPluginsAndRouter(cfg2, pm2, rm2)
	snap2 := &Snapshot{
		Version:       "v2",
		Config:        cfg2,
		Engine:        engine2,
		RouterManager: rm2,
		PluginManager: pm2,
	}

	// 原子替换快照
	dispatcher.Store(snap2)

	// 再次请求 /api/b -> 200, /api/a -> 依然 200
	recB2 := newCloseNotifyRecorder()
	dispatcher.ServeHTTP(recB2, reqB)
	assert.Equal(t, http.StatusOK, recB2.Code)
	assert.Equal(t, "backend-B", recB2.Body.String())

	recA2 := newCloseNotifyRecorder()
	dispatcher.ServeHTTP(recA2, reqA)
	assert.Equal(t, http.StatusOK, recA2.Code)
	assert.Equal(t, "backend-A", recA2.Body.String())
}

// TestRouterManagerLiveTrafficConcurrency 验证高并发请求下 router.Manager 读写与转发无竞态 (-race 验证)
func TestRouterManagerLiveTrafficConcurrency(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Routes: []config.RouteConfig{
			{
				Name: "route-concurrency",
				Match: config.RouteMatch{
					Type: "prefix",
					Path: "/concurrent",
				},
				Target: config.TargetConfig{URL: backend.URL},
			},
		},
	}

	pm := plugin.NewManager()
	rm := router.NewManagerWithConfig(cfg)
	engine := buildEngineWithConfigPluginsAndRouter(cfg, pm, rm)
	snap := &Snapshot{
		Version:       "v1",
		Config:        cfg,
		Engine:        engine,
		RouterManager: rm,
		PluginManager: pm,
	}
	dispatcher := NewDispatcher(snap)

	const goroutines = 20
	const requestsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				path := fmt.Sprintf("/concurrent/worker-%d/req-%d", id, j)
				req := httptest.NewRequest("GET", path, nil)
				rec := newCloseNotifyRecorder()

				dispatcher.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("并发请求失败: code=%d, path=%s", rec.Code, path)
					return
				}
			}
		}(i)
	}

	wg.Wait()
}
