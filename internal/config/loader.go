package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// LoadConfig 加载配置文件
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// 设置配置文件搜索路径
	if configPath != "" {
		v.AddConfigPath(configPath)
	} else {
		// 默认搜索路径
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/gateway")
	}

	// 环境变量支持
	v.SetEnvPrefix("GATEWAY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
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

	if err := decoder.Decode(v.AllSettings()); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证配置
	if err := ValidateConfig(&config); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return &config, nil
}

// validateConfig 验证配置
func validateConfig(config *Config) error {
	// 验证服务器配置
	if config.Server.Port <= 0 {
		return fmt.Errorf("服务器端口必须大于0")
	}

	// 验证日志配置
	if config.Log.Level != "" {
		validLevels := map[string]bool{
			"debug": true,
			"info":  true,
			"warn":  true,
			"error": true,
		}
		if !validLevels[config.Log.Level] {
			return fmt.Errorf("无效的日志级别: %s", config.Log.Level)
		}
	}

	// 验证路由配置
	for _, route := range config.Routes {
		if route.Name == "" {
			return fmt.Errorf("路由名称不能为空")
		}
		if route.Target.URL == "" {
			return fmt.Errorf("路由目标URL不能为空")
		}
		if route.Match.Path == "" {
			return fmt.Errorf("路由路径不能为空")
		}
	}

	return nil
}

// GetConfigPath 获取配置文件路径
func GetConfigPath() string {
	// 1. 检查环境变量
	if path := os.Getenv("GATEWAY_CONFIG_PATH"); path != "" {
		return path
	}

	// 2. 检查当前目录
	if _, err := os.Stat("config/config.yaml"); err == nil {
		return "config"
	}

	// 3. 检查可执行文件所在目录
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(exeDir, "config", "config.yaml")); err == nil {
			return filepath.Join(exeDir, "config")
		}
	}

	// 4. 返回默认路径
	return "config"
}
