package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gateway-go/internal/config"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// writePluginTestConfigFile 写入包含插件定义的测试配置文件
func writePluginTestConfigFile(t *testing.T, dir string, targetURL string, availablePluginsYAML string, routePluginsYAML string) string {
	t.Helper()
	cfgContent := fmt.Sprintf(`server:
  port: 8080
  mode: "test"
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 30s
  max_header_bytes: 1048576
  graceful_shutdown_timeout: 5s
log:
  level: "info"
  format: "json"
  output: "stdout"
  max_size: 100
  max_age: 30
  max_backups: 10
plugins:
  available:
%s
routes:
  - name: "plugin-test-route"
    match:
      type: "prefix"
      path: "/api"
      priority: 10
    target:
      url: "%s"
%s
`, availablePluginsYAML, targetURL, routePluginsYAML)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}
	return cfgPath
}

// TestPluginRemovedOnReload 验证插件从 available 移除后，路由引用该插件导致重载失败并保留旧配置 (REQ-P1-02)
func TestPluginRemovedOnReload(t *testing.T) {
	SetSnapshotGracePeriod(20 * time.Millisecond)
	defer SetSnapshotGracePeriod(2 * time.Second)

	// 1. 启动模拟上游
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream":"active"}`))
	}))
	defer upstream.Close()

	tempDir := t.TempDir()

	// 2. 初始配置：available 包含 rate_limit 与 cors；路由引用 rate_limit 与 cors
	initialAvailableYAML := `    - name: "rate_limit"
      enabled: true
      order: 10
      config:
        requests_per_second: 100.0
        burst: 100
    - name: "cors"
      enabled: true
      order: 20
      config:
        allowed_origins:
          - "*"`
	initialRoutePluginsYAML := `    plugins:
      - "rate_limit"
      - "cors"`

	cfgPath := writePluginTestConfigFile(t, tempDir, upstream.URL, initialAvailableYAML, initialRoutePluginsYAML)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载初始配置失败: %v", err)
	}

	// 3. 构建初始快照并启动分发器与网关服务
	initialSnap, err := buildSnapshotForConfig(cm.GetConfig(), cm.GetVersion())
	if err != nil {
		t.Fatalf("构建初始快照失败: %v", err)
	}
	dispatcher := NewDispatcher(initialSnap)
	setupReloadLifecycle(cm, dispatcher)

	gwServer := httptest.NewServer(dispatcher)
	defer gwServer.Close()

	client := gwServer.Client()

	// 4. 验证初始请求正常通过
	resp1, err := client.Get(gwServer.URL + "/api/test")
	if err != nil || resp1.StatusCode != http.StatusOK {
		t.Fatalf("初次请求失败: err=%v, code=%d", err, resp1.StatusCode)
	}
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if !strings.Contains(string(body1), "active") {
		t.Fatalf("初次请求响应内容不匹配: %s", string(body1))
	}

	initialVersion := cm.GetVersion()
	if initialVersion != "1" {
		t.Fatalf("期望初始配置版本为 1，实际为: %s", initialVersion)
	}

	// 5. 阶段一：从 available 中移除 rate_limit (只保留 cors)，但路由仍引用 rate_limit
	// 期望：热重载失败，版本号不变，旧快照继续生效提供服务
	invalidAvailableYAML := `    - name: "cors"
      enabled: true
      order: 20
      config:
        allowed_origins:
          - "*"`
	// 路由仍旧引用 rate_limit
	stillReferencingRoutePluginsYAML := `    plugins:
      - "rate_limit"
      - "cors"`

	writePluginTestConfigFile(t, tempDir, upstream.URL, invalidAvailableYAML, stillReferencingRoutePluginsYAML)

	reloadErr := cm.ReloadConfig()
	if reloadErr == nil {
		t.Fatal("期望重载失败（路由使用了未在 available 中启用的插件），但实际返回 nil")
	}

	// 检查报错信息是否明确指明插件不可用
	if !strings.Contains(reloadErr.Error(), "不可用") && !strings.Contains(reloadErr.Error(), "rate_limit") {
		t.Fatalf("重载错误信息未明确指明 rate_limit 不可用: %v", reloadErr)
	}

	// 校验配置版本号完全未变（触发了回滚）
	afterFailedVersion := cm.GetVersion()
	if afterFailedVersion != initialVersion {
		t.Fatalf("回滚失败：配置版本号发生变化，期望 %s，实际 %s", initialVersion, afterFailedVersion)
	}

	// 校验活跃快照版本号未变
	currentSnap := dispatcher.Snapshot()
	if currentSnap.Version != initialVersion {
		t.Fatalf("回滚失败：分发器快照版本发生变化，期望 %s，实际 %s", initialVersion, currentSnap.Version)
	}

	// 校验网关仍可正常处理请求（旧配置继续生效）
	resp2, err := client.Get(gwServer.URL + "/api/test")
	if err != nil || resp2.StatusCode != http.StatusOK {
		t.Fatalf("回滚后旧配置处理请求失败: err=%v, code=%d", err, resp2.StatusCode)
	}
	_ = resp2.Body.Close()

	// 6. 阶段二：修正配置，从路由中也移除 rate_limit，只引用 cors
	// 期望：热重载成功，版本号递增为 2，rate_limit 彻底被摘除并 Stop
	validRoutePluginsYAML := `    plugins:
      - "cors"`

	writePluginTestConfigFile(t, tempDir, upstream.URL, invalidAvailableYAML, validRoutePluginsYAML)

	reloadSuccessErr := cm.ReloadConfig()
	if reloadSuccessErr != nil {
		t.Fatalf("合法重载失败: %v", reloadSuccessErr)
	}

	// 校验版本号递增为 2
	newVersion := cm.GetVersion()
	if newVersion != "2" {
		t.Fatalf("期望重载后版本号为 2，实际为: %s", newVersion)
	}
	if dispatcher.Snapshot().Version != "2" {
		t.Fatalf("期望分发器快照版本号为 2，实际为: %s", dispatcher.Snapshot().Version)
	}

	// 校验新快照中 rate_limit 已不存在于可用插件中
	activeSnap := dispatcher.Snapshot()
	if activeSnap.PluginManager != nil {
		if _, exists := activeSnap.PluginManager.GetAvailablePlugin("rate_limit"); exists {
			t.Fatalf("rate_limit 应已从新快照可用插件中移除")
		}
		if _, exists := activeSnap.PluginManager.GetAvailablePlugin("cors"); !exists {
			t.Fatalf("cors 应在新快照可用插件中")
		}
	}

	// 校验新快照正常处理请求
	resp3, err := client.Get(gwServer.URL + "/api/test")
	if err != nil || resp3.StatusCode != http.StatusOK {
		t.Fatalf("新快照处理请求失败: err=%v, code=%d", err, resp3.StatusCode)
	}
	_ = resp3.Body.Close()
}

// TestPluginDisabledOnReloadRouteReferences 验证插件设为 enabled: false 时路由引用同样导致重载失败并保留旧配置 (REQ-P1-02)
func TestPluginDisabledOnReloadRouteReferences(t *testing.T) {
	SetSnapshotGracePeriod(20 * time.Millisecond)
	defer SetSnapshotGracePeriod(2 * time.Second)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	tempDir := t.TempDir()

	initialAvailableYAML := `    - name: "circuit_breaker"
      enabled: true
      order: 10
      config:
        failure_threshold: 50`
	initialRoutePluginsYAML := `    plugins:
      - "circuit_breaker"`

	cfgPath := writePluginTestConfigFile(t, tempDir, upstream.URL, initialAvailableYAML, initialRoutePluginsYAML)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	initialSnap, err := buildSnapshotForConfig(cm.GetConfig(), cm.GetVersion())
	if err != nil {
		t.Fatalf("构建初始快照失败: %v", err)
	}
	dispatcher := NewDispatcher(initialSnap)
	setupReloadLifecycle(cm, dispatcher)

	gwServer := httptest.NewServer(dispatcher)
	defer gwServer.Close()

	// 禁用 circuit_breaker (enabled: false) 但路由仍引用它
	disabledAvailableYAML := `    - name: "circuit_breaker"
      enabled: false
      order: 10
      config:
        failure_threshold: 50`

	writePluginTestConfigFile(t, tempDir, upstream.URL, disabledAvailableYAML, initialRoutePluginsYAML)

	err = cm.ReloadConfig()
	if err == nil {
		t.Fatal("期望重载失败，因为引用的插件已禁用，但实际返回 nil")
	}

	if cm.GetVersion() != "1" {
		t.Fatalf("重载失败后版本应维持 1，实际为 %s", cm.GetVersion())
	}
	if dispatcher.Snapshot().Version != "1" {
		t.Fatalf("快照版本应维持 1，实际为 %s", dispatcher.Snapshot().Version)
	}

	// 旧配置依然生效
	resp, err := gwServer.Client().Get(gwServer.URL + "/api")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("请求旧路由失败: err=%v, code=%d", err, resp.StatusCode)
	}
	_ = resp.Body.Close()
}
