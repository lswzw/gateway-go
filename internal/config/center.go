package config

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// ConfigVersion 配置版本信息
type ConfigVersion struct {
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Config    Config    `json:"config"`
	Comment   string    `json:"comment"`
}

// ConfigCenter 配置中心管理器
type ConfigCenter struct {
	currentConfig *Config
	versions      []ConfigVersion
	maxVersions   int
	mu            sync.RWMutex
	viper         *viper.Viper
	notifyChan    chan ConfigVersion
}

// NewConfigCenter 创建配置中心管理器
func NewConfigCenter(maxVersions int) *ConfigCenter {
	return &ConfigCenter{
		maxVersions: maxVersions,
		viper:       viper.New(),
		notifyChan:  make(chan ConfigVersion, 100),
	}
}

// Init 初始化配置中心
func (cc *ConfigCenter) Init(configPath string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// 设置配置文件
	cc.viper.SetConfigFile(configPath)
	cc.viper.SetConfigType("yaml")

	// 环境变量支持
	cc.viper.SetEnvPrefix("GATEWAY")
	cc.viper.AutomaticEnv()

	// 读取配置文件
	if err := cc.viper.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	fmt.Printf("[DEBUG] viper.AllSettings: %+v\n", cc.viper.AllSettings())

	// 调试输出，查看read_timeout类型和值
	fmt.Printf("[DEBUG] read_timeout type: %T, value: %#v\n", cc.viper.Get("server.read_timeout"), cc.viper.Get("server.read_timeout"))

	// 解析配置，增强DecodeHook支持数字转time.Duration
	var config Config
	decodeHook := mapstructure.ComposeDecodeHookFunc(
		// 支持字符串和数字转time.Duration
		func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
			if t == reflect.TypeOf(time.Duration(0)) {
				switch v := data.(type) {
				case string:
					d, err := time.ParseDuration(v)
					if err != nil {
						fmt.Printf("[DEBUG] time.ParseDuration(%q) err: %v\n", v, err)
					}
					return d, err
				case int, int64, float64:
					sec := reflect.ValueOf(v).Convert(reflect.TypeOf(int64(0))).Int()
					return time.Duration(sec) * time.Second, nil
				}
			}
			return data, nil
		},
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)
	decoderConfig := &mapstructure.DecoderConfig{
		DecodeHook: decodeHook,
		Result:     &config,
	}
	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return fmt.Errorf("创建解码器失败: %w", err)
	}
	if err := decoder.Decode(cc.viper.AllSettings()); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 解析后调试输出
	fmt.Printf("[DEBUG] config.Server.ReadTimeout type: %T, value: %#v\n", config.Server.ReadTimeout, config.Server.ReadTimeout)

	// 验证配置
	if err := ValidateConfig(&config); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	// 保存当前配置
	cc.currentConfig = &config

	// 添加初始版本
	cc.addVersion("initial", "初始配置")

	return nil
}

// WatchConfig 监视配置变化
func (cc *ConfigCenter) WatchConfig() {
	cc.viper.WatchConfig()
	cc.viper.OnConfigChange(func(e fsnotify.Event) {
		cc.mu.Lock()
		defer cc.mu.Unlock()

		// 读取新配置
		var newConfig Config
		if err := cc.viper.Unmarshal(&newConfig); err != nil {
			fmt.Printf("解析新配置失败: %v\n", err)
			return
		}

		// 验证新配置
		if err := ValidateConfig(&newConfig); err != nil {
			fmt.Printf("新配置验证失败: %v\n", err)
			return
		}

		// 更新配置
		cc.currentConfig = &newConfig

		// 添加新版本
		version := cc.addVersion("auto", "自动更新")

		// 发送通知
		select {
		case cc.notifyChan <- version:
		default:
			// 通道已满，丢弃通知
		}
	})
}

// GetCurrentConfig 获取当前配置
func (cc *ConfigCenter) GetCurrentConfig() *Config {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.currentConfig
}

// UpdateConfig 更新配置（支持部分更新）
func (cc *ConfigCenter) UpdateConfig(newConfig *Config, comment string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// 获取当前配置
	currentConfig := cc.currentConfig
	if currentConfig == nil {
		return fmt.Errorf("当前配置未初始化")
	}

	// 创建合并后的配置
	mergedConfig := *currentConfig

	// 合并日志配置 - 只更新非零值
	if newConfig.Log.Level != "" {
		mergedConfig.Log.Level = newConfig.Log.Level
	}
	if newConfig.Log.Format != "" {
		mergedConfig.Log.Format = newConfig.Log.Format
	}
	if newConfig.Log.Output != "" {
		mergedConfig.Log.Output = newConfig.Log.Output
	}
	if newConfig.Log.MaxSize > 0 {
		mergedConfig.Log.MaxSize = newConfig.Log.MaxSize
	}
	if newConfig.Log.MaxAge > 0 {
		mergedConfig.Log.MaxAge = newConfig.Log.MaxAge
	}
	if newConfig.Log.MaxBackups > 0 {
		mergedConfig.Log.MaxBackups = newConfig.Log.MaxBackups
	}

	// 合并插件配置
	if len(newConfig.Plugins.Available) > 0 {
		mergedConfig.Plugins.Available = newConfig.Plugins.Available
	}

	// 合并路由配置 - 只更新非空值
	if len(newConfig.Routes) > 0 {
		mergedConfig.Routes = newConfig.Routes
	}

	// 合并服务器配置 - 只更新可热更新的配置项
	if newConfig.Server.ReadTimeout != 0 {
		mergedConfig.Server.ReadTimeout = newConfig.Server.ReadTimeout
	}
	if newConfig.Server.WriteTimeout != 0 {
		mergedConfig.Server.WriteTimeout = newConfig.Server.WriteTimeout
	}
	if newConfig.Server.MaxHeaderBytes > 0 {
		mergedConfig.Server.MaxHeaderBytes = newConfig.Server.MaxHeaderBytes
	}
	if newConfig.Server.GracefulShutdownTimeout > 0 {
		mergedConfig.Server.GracefulShutdownTimeout = newConfig.Server.GracefulShutdownTimeout
	}

	// 验证合并后的配置
	if err := ValidateConfig(&mergedConfig); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	// 更新配置
	cc.currentConfig = &mergedConfig

	// 添加新版本
	version := cc.addVersion("manual", comment)

	// 发送通知
	select {
	case cc.notifyChan <- version:
	default:
		// 通道已满，丢弃通知
	}

	return nil
}

// RollbackConfig 回滚配置
func (cc *ConfigCenter) RollbackConfig(version string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// 查找指定版本
	var targetVersion *ConfigVersion
	for _, v := range cc.versions {
		if v.Version == version {
			targetVersion = &v
			break
		}
	}

	if targetVersion == nil {
		return fmt.Errorf("未找到指定版本: %s", version)
	}

	// 更新配置
	cc.currentConfig = &targetVersion.Config

	// 添加回滚版本
	cc.addVersion("rollback", fmt.Sprintf("回滚到版本 %s", version))

	return nil
}

// GetVersions 获取配置版本历史
func (cc *ConfigCenter) GetVersions() []ConfigVersion {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	versions := make([]ConfigVersion, len(cc.versions))
	copy(versions, cc.versions)
	return versions
}

// GetVersion 获取指定版本的配置
func (cc *ConfigCenter) GetVersion(version string) (*ConfigVersion, error) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	for _, v := range cc.versions {
		if v.Version == version {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("未找到指定版本: %s", version)
}

// Subscribe 订阅配置变更通知
func (cc *ConfigCenter) Subscribe() <-chan ConfigVersion {
	return cc.notifyChan
}

// addVersion 添加配置版本
func (cc *ConfigCenter) addVersion(source, comment string) ConfigVersion {
	version := ConfigVersion{
		Version:   fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Config:    *cc.currentConfig,
		Comment:   fmt.Sprintf("[%s] %s", source, comment),
	}

	// 添加到版本历史
	cc.versions = append(cc.versions, version)

	// 如果超过最大版本数，删除最旧的版本
	if len(cc.versions) > cc.maxVersions {
		cc.versions = cc.versions[1:]
	}

	return version
}
