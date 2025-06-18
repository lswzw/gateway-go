package config

import (
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// Config 总配置结构
type Config struct {
	Server  ServerConfig  `yaml:"server" mapstructure:"server"`
	Log     LogConfig     `yaml:"log" mapstructure:"log"`
	Plugins PluginsConfig `yaml:"plugins" mapstructure:"plugins"`
	Routes  []RouteConfig `yaml:"routes" mapstructure:"routes"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port                    int           `yaml:"port" mapstructure:"port"`
	Mode                    string        `yaml:"mode" mapstructure:"mode"`
	ReadTimeout             time.Duration `yaml:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout            time.Duration `yaml:"write_timeout" mapstructure:"write_timeout"`
	MaxHeaderBytes          int           `yaml:"max_header_bytes" mapstructure:"max_header_bytes"`
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout" mapstructure:"graceful_shutdown_timeout"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `yaml:"level" mapstructure:"level"`
	Format     string `yaml:"format" mapstructure:"format"`
	Output     string `yaml:"output" mapstructure:"output"`
	MaxSize    int    `yaml:"max_size" mapstructure:"max_size"`
	MaxAge     int    `yaml:"max_age" mapstructure:"max_age"`
	MaxBackups int    `yaml:"max_backups" mapstructure:"max_backups"`
	Compress   bool   `yaml:"compress" mapstructure:"compress"`
}

// PluginsConfig 插件配置
type PluginsConfig struct {
	Available []PluginConfig            `yaml:"available" mapstructure:"available"`
	Routes    map[string][]PluginConfig `yaml:"routes" mapstructure:"routes"`
}

// PluginConfig 插件配置
type PluginConfig struct {
	Name    string                 `yaml:"name" mapstructure:"name"`
	Enabled bool                   `yaml:"enabled" mapstructure:"enabled"`
	Order   int                    `yaml:"order" mapstructure:"order"`
	Config  map[string]interface{} `yaml:"config" mapstructure:"config"`
}

// RouteMatch 路由匹配规则
type RouteMatch struct {
	Type        string            `yaml:"type" mapstructure:"type"`
	Path        string            `yaml:"path" mapstructure:"path"`
	Priority    int               `yaml:"priority" mapstructure:"priority"`
	Host        string            `yaml:"host" mapstructure:"host"`
	Method      string            `yaml:"method" mapstructure:"method"`
	Headers     map[string]string `yaml:"headers" mapstructure:"headers"`
	QueryParams map[string]string `yaml:"query_params" mapstructure:"query_params"`
}

// RouteConfig 路由配置
type RouteConfig struct {
	Name     string          `yaml:"name" mapstructure:"name"`
	Match    RouteMatch      `yaml:"match" mapstructure:"match"`
	Target   TargetConfig    `yaml:"target" mapstructure:"target"`
	Plugins  []string        `yaml:"plugins" mapstructure:"plugins"`
	Response *ResponseConfig `yaml:"response" mapstructure:"response"`
}

// ResponseConfig 响应配置
type ResponseConfig struct {
	Status      int    `yaml:"status" mapstructure:"status"`
	Content     string `yaml:"content" mapstructure:"content"`
	ContentType string `yaml:"content_type" mapstructure:"content_type"`
}

// TargetConfig 目标服务配置
type TargetConfig struct {
	// 服务地址
	URL string `yaml:"url" mapstructure:"url"`
	// 超时时间（毫秒）
	Timeout int `yaml:"timeout" mapstructure:"timeout"`
	// 重试次数
	Retries int `yaml:"retries" mapstructure:"retries"`
}

var GlobalConfig Config

func Init() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// 设置自动解析时间字符串
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	// 使用 DecoderConfig 来确保正确解析时间字符串
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
		return err
	}

	if err := decoder.Decode(viper.AllSettings()); err != nil {
		return err
	}

	GlobalConfig = config
	return nil
}
