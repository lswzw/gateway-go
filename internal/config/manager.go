package config

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// ConfigManager 配置管理器 - 类似nginx的配置管理
type ConfigManager struct {
	configPath    string
	currentConfig *Config
	version       string
	versionSeq    int64
	viper         *viper.Viper
	watcher       *fsnotify.Watcher
	mu            sync.RWMutex
	reloadChan    chan struct{}
	stopChan      chan struct{}
	reloadHooks   []func(*Config) error

	// 快照重载生命周期钩子
	prepareHook   func(newCfg *Config, nextVersion string) error
	commitHook    func(newCfg *Config, nextVersion string) error
	rollbackHook  func(newCfg *Config, err error)
	onReloadEvent func(version string, duration time.Duration, success bool, err error)
}

// NewConfigManager 创建配置管理器
func NewConfigManager(configPath string) *ConfigManager {
	return &ConfigManager{
		configPath:  configPath,
		version:     "1",
		versionSeq:  1,
		viper:       viper.New(),
		reloadChan:  make(chan struct{}, 1),
		stopChan:    make(chan struct{}),
		reloadHooks: make([]func(*Config) error, 0),
	}
}

// ParseConfigFile 解析并验证指定的配置文件，但不更新当前状态
func (cm *ConfigManager) ParseConfigFile(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = cm.configPath
	}

	testViper := viper.New()
	testViper.SetConfigFile(configPath)
	testViper.SetConfigType("yaml")

	// 环境变量支持
	testViper.SetEnvPrefix("GATEWAY")
	testViper.AutomaticEnv()

	// 读取配置文件
	if err := testViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析配置
	var config Config
	decoderConfig := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
		Result: &config,
	}

	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, fmt.Errorf("创建解码器失败: %w", err)
	}

	if err := decoder.Decode(testViper.AllSettings()); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证配置
	if err := ValidateConfig(&config); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return &config, nil
}

// TestConfig 测试配置文件 - 类似 nginx -t
func (cm *ConfigManager) TestConfig(configPath string) error {
	cfg, err := cm.ParseConfigFile(configPath)
	if err != nil {
		return err
	}

	warnings := DetectShadowedRoutes(cfg.Routes)
	for _, w := range warnings {
		fmt.Printf("⚠ 警告: 检测到路由遮蔽: 路由 [%s] 遮蔽了路由 [%s] (%s)\n", w.ShadowRoute, w.ShadowedRoute, w.Reason)
	}

	fmt.Printf("✓ 配置文件 %s 语法正确\n", configPath)
	return nil
}

// LoadConfig 加载配置文件
func (cm *ConfigManager) LoadConfig(configPath string) error {
	if configPath == "" {
		configPath = cm.configPath
	}

	cfg, err := cm.ParseConfigFile(configPath)
	if err != nil {
		return err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.configPath = configPath
	cm.currentConfig = cfg
	if cm.version == "" {
		cm.versionSeq = 1
		cm.version = "1"
	}

	fmt.Printf("✓ 配置文件 %s 加载成功\n", configPath)
	return nil
}

// ReloadConfig 热重载配置 - 类似 nginx -s reload
func (cm *ConfigManager) ReloadConfig() error {
	startTime := time.Now()
	fmt.Println("正在重新加载配置...")

	// 1. 测试并解析新配置语法 (如果失败，不改变任何现有状态)
	newConfig, err := cm.ParseConfigFile(cm.configPath)
	if err != nil {
		cm.mu.RLock()
		oldVer := cm.version
		if oldVer == "" {
			oldVer = "1"
		}
		listener := cm.onReloadEvent
		cm.mu.RUnlock()

		duration := time.Since(startTime)
		if listener != nil {
			listener(oldVer, duration, false, err)
		}
		return fmt.Errorf("配置测试失败: %w", err)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	currentVer := cm.version
	if currentVer == "" {
		currentVer = "1"
		cm.versionSeq = 1
	}
	nextVersion := fmt.Sprintf("%d", cm.versionSeq+1)

	// 2. 准备阶段：构建新路由快照与插件引擎
	if cm.prepareHook != nil {
		if err := cm.prepareHook(newConfig, nextVersion); err != nil {
			if cm.rollbackHook != nil {
				cm.rollbackHook(newConfig, err)
			}
			duration := time.Since(startTime)
			if cm.onReloadEvent != nil {
				cm.onReloadEvent(currentVer, duration, false, err)
			}
			return fmt.Errorf("准备新配置快照失败: %w", err)
		}
	}

	// 3. 执行重载钩子。若任何钩子失败，执行回滚并保留旧快照与版本号
	for _, hook := range cm.reloadHooks {
		if err := hook(newConfig); err != nil {
			// 回滚：不应用新快照，保留旧配置与版本号
			if cm.rollbackHook != nil {
				cm.rollbackHook(newConfig, err)
			}
			duration := time.Since(startTime)
			if cm.onReloadEvent != nil {
				cm.onReloadEvent(currentVer, duration, false, err)
			}
			return fmt.Errorf("重载钩子执行失败: %w", err)
		}
	}

	// 4. 所有钩子成功，提交快照原子切换
	if cm.commitHook != nil {
		if err := cm.commitHook(newConfig, nextVersion); err != nil {
			if cm.rollbackHook != nil {
				cm.rollbackHook(newConfig, err)
			}
			duration := time.Since(startTime)
			if cm.onReloadEvent != nil {
				cm.onReloadEvent(currentVer, duration, false, err)
			}
			return fmt.Errorf("提交新配置快照失败: %w", err)
		}
	}

	// 5. 更新配置与版本号
	cm.currentConfig = newConfig
	cm.versionSeq++
	cm.version = nextVersion

	duration := time.Since(startTime)
	if cm.onReloadEvent != nil {
		cm.onReloadEvent(cm.version, duration, true, nil)
	}

	fmt.Printf("✓ 配置重载成功 (版本: %s, 耗时: %v)\n", cm.version, duration)
	return nil
}

// GetVersion 获取当前配置版本号
func (cm *ConfigManager) GetVersion() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.version == "" {
		return "1"
	}
	return cm.version
}

// SetVersion 设置配置版本号 (供测试或初始化使用)
func (cm *ConfigManager) SetVersion(v string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.version = v
}

// SetReloadLifecycle 设置重载生命周期回调 (用于路由快照构建、提交与回滚)
func (cm *ConfigManager) SetReloadLifecycle(
	prepare func(newCfg *Config, nextVersion string) error,
	commit func(newCfg *Config, nextVersion string) error,
	rollback func(newCfg *Config, err error),
) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.prepareHook = prepare
	cm.commitHook = commit
	cm.rollbackHook = rollback
}

// SetReloadEventListener 设置重载事件监听器 (用于指标打点与日志)
func (cm *ConfigManager) SetReloadEventListener(listener func(version string, duration time.Duration, success bool, err error)) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onReloadEvent = listener
}

// GetConfig 获取当前配置
func (cm *ConfigManager) GetConfig() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.currentConfig
}

// SetConfig 设置配置（供测试或程序化配置使用）
func (cm *ConfigManager) SetConfig(cfg *Config) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.currentConfig = cfg
	if cm.version == "" {
		cm.versionSeq = 1
		cm.version = "1"
	}
}

// AddReloadHook 添加重载钩子
func (cm *ConfigManager) AddReloadHook(hook func(*Config) error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.reloadHooks = append(cm.reloadHooks, hook)
}

// WatchConfig 监视配置文件变化
func (cm *ConfigManager) WatchConfig() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建文件监视器失败: %w", err)
	}

	cm.watcher = watcher

	// 监视配置文件所在目录
	configDir := filepath.Dir(cm.configPath)
	if err := watcher.Add(configDir); err != nil {
		return fmt.Errorf("添加监视目录失败: %w", err)
	}

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Name == cm.configPath && (event.Op&fsnotify.Write == fsnotify.Write) {
					fmt.Printf("检测到配置文件变化: %s\n", event.Name)

					// 延迟重载，避免文件写入未完成
					time.Sleep(100 * time.Millisecond)

					// 发送重载信号
					select {
					case cm.reloadChan <- struct{}{}:
					default:
						// 通道已满，忽略
					}
				}
			case err := <-watcher.Errors:
				fmt.Printf("文件监视错误: %v\n", err)
			case <-cm.stopChan:
				return
			}
		}
	}()

	return nil
}

// StartReloadWorker 启动重载工作协程
func (cm *ConfigManager) StartReloadWorker() {
	go func() {
		for {
			select {
			case <-cm.reloadChan:
				if err := cm.ReloadConfig(); err != nil {
					fmt.Printf("配置重载失败: %v\n", err)
				}
			case <-cm.stopChan:
				return
			}
		}
	}()
}

// Stop 停止配置管理器
func (cm *ConfigManager) Stop() {
	close(cm.stopChan)
	if cm.watcher != nil {
		cm.watcher.Close()
	}
}

// HandleSignals 处理系统信号
func (cm *ConfigManager) HandleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGUSR1)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP, syscall.SIGUSR1:
				fmt.Printf("收到信号 %v，重新加载配置\n", sig)
				if err := cm.ReloadConfig(); err != nil {
					fmt.Printf("配置重载失败: %v\n", err)
				}
			}
		}
	}()
}

// ValidateConfigFile 验证配置文件
func (cm *ConfigManager) ValidateConfigFile(configPath string) error {
	return cm.TestConfig(configPath)
}

// GetConfigPath 获取配置文件路径
func (cm *ConfigManager) GetConfigPath() string {
	return cm.configPath
}

// SetConfigPath 设置配置文件路径
func (cm *ConfigManager) SetConfigPath(configPath string) {
	cm.configPath = configPath
}
