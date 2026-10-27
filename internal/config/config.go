package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// Config 总配置结构
type Config struct {
	Version string        `yaml:"version" mapstructure:"version"`
	Server  ServerConfig  `yaml:"server" mapstructure:"server"`
	Admin   AdminConfig   `yaml:"admin" mapstructure:"admin"`
	Log     LogConfig     `yaml:"log" mapstructure:"log"`
	Plugins PluginsConfig `yaml:"plugins" mapstructure:"plugins"`
	Routes  []RouteConfig `yaml:"routes" mapstructure:"routes"`
}

// GetVersion 获取配置版本
func (c *Config) GetVersion() string {
	if c != nil && c.Version != "" {
		return c.Version
	}
	return "1.0.0"
}

// AdminConfig 管理端口配置 (REQ-P1-04)
type AdminConfig struct {
	Listen string `yaml:"listen" mapstructure:"listen"`
}

func (a *AdminConfig) GetListen() string {
	if a.Listen != "" {
		return a.Listen
	}
	return "127.0.0.1:9090"
}

// TLSConfig TLS 证书配置
type TLSConfig struct {
	CertFile string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile  string `yaml:"key_file" mapstructure:"key_file"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port                    int           `yaml:"port" mapstructure:"port"`
	Mode                    string        `yaml:"mode" mapstructure:"mode"`
	ReadTimeout             time.Duration `yaml:"read_timeout" mapstructure:"read_timeout"`
	ReadHeaderTimeout       time.Duration `yaml:"read_header_timeout" mapstructure:"read_header_timeout"`
	WriteTimeout            time.Duration `yaml:"write_timeout" mapstructure:"write_timeout"`
	IdleTimeout             time.Duration `yaml:"idle_timeout" mapstructure:"idle_timeout"`
	MaxHeaderBytes          int           `yaml:"max_header_bytes" mapstructure:"max_header_bytes"`
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout" mapstructure:"graceful_shutdown_timeout"`
	PIDFile                 string        `yaml:"pid_file" mapstructure:"pid_file"`
	TrustedProxies          []string      `yaml:"trusted_proxies" mapstructure:"trusted_proxies"`
	MaxBodyBytes            int64         `yaml:"max_body_bytes" mapstructure:"max_body_bytes"`
	TLS                     TLSConfig     `yaml:"tls" mapstructure:"tls"`
}

func (s *ServerConfig) GetMaxBodyBytes() int64 {
	if s.MaxBodyBytes > 0 {
		return s.MaxBodyBytes
	}
	return 1048576 // 默认 1MB
}

// GetReadHeaderTimeout 获取实际生效的 ReadHeaderTimeout
// 如果未配置或<=0，则默认为 ReadTimeout 的一半
func (s *ServerConfig) GetReadHeaderTimeout() time.Duration {
	if s.ReadHeaderTimeout > 0 {
		return s.ReadHeaderTimeout
	}
	return s.ReadTimeout / 2
}

// GetPIDFile 获取 PID 文件路径
// 优先使用配置的 PIDFile，未配置时回退到 /tmp/gateway-go-<port>.pid 或 /tmp/gateway.pid
func (s *ServerConfig) GetPIDFile() string {
	if s.PIDFile != "" {
		return s.PIDFile
	}
	if s.Port > 0 {
		return fmt.Sprintf("/tmp/gateway-go-%d.pid", s.Port)
	}
	return "/tmp/gateway.pid"
}

// LogConfig 日志配置
type LogConfig struct {
	Level        string   `yaml:"level" mapstructure:"level"`
	Format       string   `yaml:"format" mapstructure:"format"`
	Output       string   `yaml:"output" mapstructure:"output"`
	MaxSize      int      `yaml:"max_size" mapstructure:"max_size"`
	MaxAge       int      `yaml:"max_age" mapstructure:"max_age"`
	MaxBackups   int      `yaml:"max_backups" mapstructure:"max_backups"`
	Compress     bool     `yaml:"compress" mapstructure:"compress"`
	BodyLimit    int      `yaml:"body_limit" mapstructure:"body_limit"`
	BodySHA256   *bool    `yaml:"body_sha256" mapstructure:"body_sha256"`
	Access       *bool    `yaml:"access" mapstructure:"access"`
	AccessFields []string `yaml:"access_fields" mapstructure:"access_fields"`
}

func (l *LogConfig) IsBodySHA256Enabled() bool {
	if l.BodySHA256 == nil {
		return true
	}
	return *l.BodySHA256
}

func (l *LogConfig) IsAccessLogEnabled() bool {
	if l.Access == nil {
		return true
	}
	return *l.Access
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
	Regex       string            `yaml:"regex" mapstructure:"regex"`
	Priority    int               `yaml:"priority" mapstructure:"priority"`
	Host        string            `yaml:"host" mapstructure:"host"`
	Method      string            `yaml:"method" mapstructure:"method"`
	Headers     map[string]string `yaml:"headers" mapstructure:"headers"`
	QueryParams map[string]string `yaml:"query_params" mapstructure:"query_params"`
	ABTest      *ABTestConfig     `yaml:"ab_test" mapstructure:"ab_test"`
}

// ABTestConfig A/B测试配置 (REQ-P1-03)
type ABTestConfig struct {
	Enabled      bool    `yaml:"enabled" mapstructure:"enabled"`
	GroupA       float64 `yaml:"group_a_percentage" mapstructure:"group_a_percentage"`
	GroupB       float64 `yaml:"group_b_percentage" mapstructure:"group_b_percentage"`
	GroupATarget string  `yaml:"group_a_target" mapstructure:"group_a_target"`
	GroupBTarget string  `yaml:"group_b_target" mapstructure:"group_b_target"`
	BucketBy     string  `yaml:"bucket_by" mapstructure:"bucket_by"` // 分桶依据，如 "header:X-User-ID", "ip", "cookie:session"
	WeightA      int     `yaml:"weight_a" mapstructure:"weight_a"`   // 权重模式 Group A
	WeightB      int     `yaml:"weight_b" mapstructure:"weight_b"`   // 权重模式 Group B
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
