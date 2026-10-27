package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gateway-go/internal/testutil"
)

const sampleValidConfig = `
server:
  port: 8080
  mode: debug
  read_timeout: 5s
  write_timeout: 5s
  idle_timeout: 30s
  max_header_bytes: 1048576
  graceful_shutdown_timeout: 5s
admin:
  listen: "127.0.0.1:9090"
log:
  level: debug
  format: json
  output: stdout
  max_size: 10
  max_age: 7
  max_backups: 5
  body_limit: 1024
plugins:
  available:
    - name: rate_limit
      enabled: true
      order: 1
routes:
  - name: test-route
    match:
      type: exact
      path: /api/test
      priority: 1
    target:
      url: http://127.0.0.1:8081
`

func TestConfigManager_LoadAndTestConfig(t *testing.T) {
	cfgPath := testutil.CreateTempConfigFile(t, sampleValidConfig)

	cm := NewConfigManager(cfgPath)

	// TestConfig
	if err := cm.TestConfig(cfgPath); err != nil {
		t.Fatalf("expected valid config to pass test, got %v", err)
	}

	// Test non-existent file
	if err := cm.TestConfig("/non/existent/path/config.yaml"); err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}

	// Test malformed YAML
	badYamlPath := testutil.CreateTempConfigFile(t, `server: [not valid yaml`)
	if err := cm.TestConfig(badYamlPath); err == nil {
		t.Fatalf("expected error for malformed yaml, got nil")
	}

	// Test validation error (port out of range)
	invalidValPath := testutil.CreateTempConfigFile(t, `
server:
  port: 99999
`)
	if err := cm.TestConfig(invalidValPath); err == nil {
		t.Fatalf("expected validation error for invalid port, got nil")
	}

	// LoadConfig
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("failed to load valid config: %v", err)
	}

	loaded := cm.GetConfig()
	if loaded == nil {
		t.Fatalf("expected non-nil config after load")
	}
	if loaded.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", loaded.Server.Port)
	}
	if loaded.Admin.GetListen() != "127.0.0.1:9090" {
		t.Errorf("expected admin listen 127.0.0.1:9090, got %s", loaded.Admin.GetListen())
	}
	if loaded.Server.GetMaxBodyBytes() != 1048576 {
		t.Errorf("expected default max body bytes 1048576, got %d", loaded.Server.GetMaxBodyBytes())
	}
}

func TestConfigManager_ReloadConfigSuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write initial valid config
	if err := os.WriteFile(cfgPath, []byte(sampleValidConfig), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cm := NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("failed to load initial config: %v", err)
	}

	hookCalled := false
	var reloadedPort int
	cm.AddReloadHook(func(newCfg *Config) error {
		hookCalled = true
		reloadedPort = newCfg.Server.Port
		return nil
	})

	// 1. Success case: rewrite config file with updated port
	updatedConfig := `
server:
  port: 8088
  mode: release
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 60s
  max_header_bytes: 1048576
  graceful_shutdown_timeout: 10s
admin:
  listen: "127.0.0.1:9091"
log:
  level: info
  format: json
  output: stdout
  max_size: 10
  max_age: 7
  max_backups: 5
routes:
  - name: test-route-2
    match:
      type: exact
      path: /api/v2
      priority: 2
    target:
      url: http://127.0.0.1:8082
`
	if err := os.WriteFile(cfgPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("failed to overwrite config: %v", err)
	}

	if err := cm.ReloadConfig(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	if !hookCalled {
		t.Errorf("expected reload hook to be called")
	}
	if reloadedPort != 8088 {
		t.Errorf("expected reloaded port 8088, got %d", reloadedPort)
	}
	if cm.GetConfig().Server.Port != 8088 {
		t.Errorf("expected current config port 8088, got %d", cm.GetConfig().Server.Port)
	}

	// 2. Failure case: overwrite with invalid config (port out of range)
	// Current config should remain intact (retaining 8088)!
	badConfig := `
server:
  port: 999999
`
	if err := os.WriteFile(cfgPath, []byte(badConfig), 0644); err != nil {
		t.Fatalf("failed to write bad config: %v", err)
	}

	hookCalled = false
	err := cm.ReloadConfig()
	if err == nil {
		t.Fatalf("expected ReloadConfig to fail for invalid config, got nil")
	}

	if hookCalled {
		t.Errorf("reload hook must NOT be called when reload fails")
	}
	// Old config remains unchanged
	if cm.GetConfig().Server.Port != 8088 {
		t.Errorf("expected current config to retain port 8088 after failed reload, got %d", cm.GetConfig().Server.Port)
	}
}

func TestConfigManager_GettersSettersAndDefaults(t *testing.T) {
	cm := NewConfigManager("/tmp/default-path.yaml")

	if cm.GetConfigPath() != "/tmp/default-path.yaml" {
		t.Errorf("expected /tmp/default-path.yaml, got %s", cm.GetConfigPath())
	}

	cm.SetConfigPath("/tmp/new-path.yaml")
	if cm.GetConfigPath() != "/tmp/new-path.yaml" {
		t.Errorf("expected /tmp/new-path.yaml, got %s", cm.GetConfigPath())
	}

	dummy := &Config{
		Server: ServerConfig{Port: 9000, MaxBodyBytes: 2048},
	}
	cm.SetConfig(dummy)
	if cm.GetConfig().Server.Port != 9000 {
		t.Errorf("expected port 9000, got %d", cm.GetConfig().Server.Port)
	}
	if cm.GetConfig().Server.GetMaxBodyBytes() != 2048 {
		t.Errorf("expected max body bytes 2048, got %d", cm.GetConfig().Server.GetMaxBodyBytes())
	}

	adminDefault := AdminConfig{}
	if adminDefault.GetListen() != "127.0.0.1:9090" {
		t.Errorf("expected default admin listen 127.0.0.1:9090, got %s", adminDefault.GetListen())
	}
}

func TestLoader_LoadConfigAndGetConfigPath(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(sampleValidConfig), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}

	// Test GetConfigPath with env
	t.Setenv("GATEWAY_CONFIG_PATH", "/custom/gateway/path")
	if p := GetConfigPath(); p != "/custom/gateway/path" {
		t.Errorf("expected /custom/gateway/path, got %s", p)
	}
}

func TestValidateConfig_AllBranches(t *testing.T) {
	// Base valid config struct
	baseCfg := func() *Config {
		return &Config{
			Server: ServerConfig{
				Port:                    8080,
				ReadTimeout:             5 * time.Second,
				WriteTimeout:            5 * time.Second,
				MaxHeaderBytes:          1024,
				GracefulShutdownTimeout: 5 * time.Second,
			},
			Log: LogConfig{
				Level:      "info",
				Format:     "json",
				Output:     "stdout",
				MaxSize:    10,
				MaxAge:     7,
				MaxBackups: 5,
				BodyLimit:  0,
			},
			Plugins: PluginsConfig{
				Available: []PluginConfig{
					{Name: "rate_limit", Order: 1},
				},
			},
			Routes: []RouteConfig{
				{
					Name:   "r1",
					Match:  RouteMatch{Path: "/test", Priority: 1},
					Target: TargetConfig{URL: "http://upstream"},
				},
			},
		}
	}

	// Valid config
	if err := ValidateConfig(baseCfg()); err != nil {
		t.Fatalf("expected base config to be valid, got %v", err)
	}

	// 1. Deprecated error plugin
	cfg := baseCfg()
	cfg.Plugins.Available = append(cfg.Plugins.Available, PluginConfig{Name: "error", Order: 10})
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for deprecated error plugin")
	}

	// 2. Empty plugin name
	cfg = baseCfg()
	cfg.Plugins.Available = []PluginConfig{{Name: "", Order: 1}}
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for empty plugin name")
	}

	// 3. Negative plugin order
	cfg = baseCfg()
	cfg.Plugins.Available = []PluginConfig{{Name: "p1", Order: -1}}
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for negative plugin order")
	}

	// 4. Empty routes
	cfg = baseCfg()
	cfg.Routes = nil
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for empty routes")
	}

	// 5. Route missing name
	cfg = baseCfg()
	cfg.Routes[0].Name = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for route missing name")
	}

	// 6. Route missing path
	cfg = baseCfg()
	cfg.Routes[0].Match.Path = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for route missing path")
	}

	// 7. Route missing target URL
	cfg = baseCfg()
	cfg.Routes[0].Target.URL = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for route missing target URL")
	}

	// 8. Negative route priority
	cfg = baseCfg()
	cfg.Routes[0].Match.Priority = -1
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for negative route priority")
	}

	// 9. Invalid log level
	cfg = baseCfg()
	cfg.Log.Level = "invalid-level"
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for invalid log level")
	}

	// 10. Invalid log format
	cfg = baseCfg()
	cfg.Log.Format = "xml"
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for invalid log format")
	}

	// 11. Empty log output
	cfg = baseCfg()
	cfg.Log.Output = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for empty log output")
	}

	// 12. Invalid server timeout
	cfg = baseCfg()
	cfg.Server.ReadTimeout = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for read timeout <= 0")
	}
	cfg = baseCfg()
	cfg.Server.WriteTimeout = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for write timeout <= 0")
	}
	cfg = baseCfg()
	cfg.Server.GracefulShutdownTimeout = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for graceful timeout <= 0")
	}
	cfg = baseCfg()
	cfg.Server.MaxHeaderBytes = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Errorf("expected error for max header bytes <= 0")
	}
}

// TestReloadConfig_HookMayReadConfig 回归测试：重载钩子内必须允许回调 GetConfig/GetVersion。
// 历史缺陷：ReloadConfig 全程持有 cm.mu 写锁后执行钩子，钩子内 GetConfig() 对同一把锁
// 再次 RLock（sync.RWMutex 不可重入）→ 永久自死锁，随后所有读配置的请求一起挂死。
// 这里用看门狗超时，避免缺陷回归时把测试进程永久挂住。
func TestReloadConfig_HookMayReadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(sampleValidConfig), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cm := NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	hookReadOK := false
	cm.AddReloadHook(func(newCfg *Config) error {
		// 模拟 cmd/gateway 中"同步 routerManager"的钩子：重载期间读取当前配置与版本号
		if cm.GetConfig() != nil && cm.GetVersion() != "" {
			hookReadOK = true
		}
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- cm.ReloadConfig() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reload failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReloadConfig 3s 内未返回：钩子内读取配置导致锁重入死锁")
	}

	if !hookReadOK {
		t.Errorf("expected hook to read config successfully")
	}
	if cm.GetVersion() != "2" {
		t.Errorf("expected version 2 after reload, got %s", cm.GetVersion())
	}
}

// TestReloadConfig_Serialized 重载必须串行：并发触发时版本号单调递增且不重复
func TestReloadConfig_Serialized(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(sampleValidConfig), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cm := NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	const n = 5
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { done <- cm.ReloadConfig() }()
	}

	for i := 0; i < n; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent reload failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("并发重载 5s 内未全部返回，可能存在死锁")
		}
	}

	if got := cm.GetVersion(); got != "6" {
		t.Errorf("expected version 6 after %d reloads, got %s", n, got)
	}
}
