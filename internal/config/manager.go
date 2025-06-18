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
	viper         *viper.Viper
	watcher       *fsnotify.Watcher
	mu            sync.RWMutex
	reloadChan    chan struct{}
	stopChan      chan struct{}
	reloadHooks   []func(*Config) error
}

// NewConfigManager 创建配置管理器
func NewConfigManager(configPath string) *ConfigManager {
	return &ConfigManager{
		configPath:  configPath,
		viper:       viper.New(),
		reloadChan:  make(chan struct{}, 1),
		stopChan:    make(chan struct{}),
		reloadHooks: make([]func(*Config) error, 0),
	}
}

// TestConfig 测试配置文件 - 类似 nginx -t
func (cm *ConfigManager) TestConfig(configPath string) error {
	if configPath == "" {
		configPath = cm.configPath
	}

	// 创建临时viper实例进行测试
	testViper := viper.New()
	testViper.SetConfigFile(configPath)
	testViper.SetConfigType("yaml")

	// 环境变量支持
	testViper.SetEnvPrefix("GATEWAY")
	testViper.AutomaticEnv()

	// 读取配置文件
	if err := testViper.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
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
		return fmt.Errorf("创建解码器失败: %w", err)
	}

	if err := decoder.Decode(testViper.AllSettings()); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证配置
	if err := ValidateConfig(&config); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	fmt.Printf("✓ 配置文件 %s 语法正确\n", configPath)
	return nil
}

// LoadConfig 加载配置文件
func (cm *ConfigManager) LoadConfig(configPath string) error {
	if configPath == "" {
		configPath = cm.configPath
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 设置配置文件
	cm.viper.SetConfigFile(configPath)
	cm.viper.SetConfigType("yaml")

	// 环境变量支持
	cm.viper.SetEnvPrefix("GATEWAY")
	cm.viper.AutomaticEnv()

	// 读取配置文件
	if err := cm.viper.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
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
		return fmt.Errorf("创建解码器失败: %w", err)
	}

	if err := decoder.Decode(cm.viper.AllSettings()); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证配置
	if err := ValidateConfig(&config); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	// 更新当前配置
	cm.currentConfig = &config

	fmt.Printf("✓ 配置文件 %s 加载成功\n", configPath)
	return nil
}

// ReloadConfig 热重载配置 - 类似 nginx -s reload
func (cm *ConfigManager) ReloadConfig() error {
	fmt.Println("正在重新加载配置...")

	// 测试新配置
	if err := cm.TestConfig(cm.configPath); err != nil {
		return fmt.Errorf("配置测试失败: %w", err)
	}

	// 加载新配置
	if err := cm.LoadConfig(cm.configPath); err != nil {
		return fmt.Errorf("配置加载失败: %w", err)
	}

	// 执行重载钩子
	for _, hook := range cm.reloadHooks {
		if err := hook(cm.currentConfig); err != nil {
			return fmt.Errorf("重载钩子执行失败: %w", err)
		}
	}

	fmt.Println("✓ 配置重载成功")
	return nil
}

// GetConfig 获取当前配置
func (cm *ConfigManager) GetConfig() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.currentConfig
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
