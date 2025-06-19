package interface_auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// ErrorResponse 定义错误响应结构
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// 预定义错误响应
var (
	ErrTokenMissingOrInvalid = ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "Token missing or invalid",
	}
	ErrForbiddenAccessDenied = ErrorResponse{
		Code:    http.StatusForbidden,
		Message: "Forbidden: Access denied",
	}
	ErrAuthServiceCallFailed = ErrorResponse{
		Code:    http.StatusInternalServerError,
		Message: "Auth service call failed",
	}
	ErrNoResponseBody = ErrorResponse{
		Code:    http.StatusInternalServerError,
		Message: "Internal error: no response body",
	}
	ErrUnknownResponseType = ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "response Unknown Type",
	}
)

// ConsumersConfig 认证服务配置
type ConsumersConfig struct {
	Host    string `yaml:"host" json:"host"`
	AuthAPI string `yaml:"auth_api" json:"auth_api"`
}

// Config 插件配置结构体
type Config struct {
	WhiteInterfaces []string        `yaml:"white_interfaces" json:"white_interfaces"`
	Consumers       ConsumersConfig `yaml:"consumers" json:"consumers"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		WhiteInterfaces: []string{},
		Consumers:       ConsumersConfig{},
	}
}

// Plugin 接口认证插件
type Plugin struct {
	*core.BasePlugin
	config         *Config
	whiteListRegex []*regexp.Regexp
	whiteListExact map[string]bool
	httpClient     *http.Client
	mu             sync.RWMutex
}

// New 创建插件实例
func New() *Plugin {
	return &Plugin{
		BasePlugin:     core.NewBasePlugin("interface_auth", 900, nil),
		config:         DefaultConfig(),
		whiteListExact: make(map[string]bool),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Init 初始化插件
func (p *Plugin) Init(config interface{}) error {
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}
	configBytes, err := json.Marshal(configMap)
	if err != nil {
		return fmt.Errorf("配置序列化失败: %v", err)
	}
	if err := json.Unmarshal(configBytes, p.config); err != nil {
		return fmt.Errorf("配置解析失败: %v", err)
	}
	if err := p.compileWhiteList(); err != nil {
		return fmt.Errorf("白名单正则编译失败: %v", err)
	}
	return nil
}

// Execute 执行插件
func (p *Plugin) Execute(ctx *gin.Context) error {
	path := ctx.Request.URL.Path

	// 检查是否在白名单中
	if p.isWhiteListed(path) {
		ctx.Set("plugin_result_interface_auth", "whitelist")
		return nil // 白名单直接放行
	}

	// 获取Authorization头
	token := p.getTokenFromAuthorizationHeader(ctx)
	if token == "" {
		ctx.JSON(ErrTokenMissingOrInvalid.Code, ErrTokenMissingOrInvalid)
		ctx.Abort()
		return fmt.Errorf("token缺失或无效")
	}

	// 调用外部认证服务
	if err := p.callAuthService(ctx, token); err != nil {
		return err
	}

	// 认证成功，写入缓存结果
	ctx.Set("plugin_result_interface_auth", "success")
	return nil
}

// compileWhiteList 编译白名单正则表达式
func (p *Plugin) compileWhiteList() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.whiteListRegex = nil
	p.whiteListExact = make(map[string]bool)
	for _, pattern := range p.config.WhiteInterfaces {
		if strings.Contains(pattern, "*") {
			// 将通配符转换为正则表达式
			regexPattern := "^" + strings.ReplaceAll(pattern, "*", ".*") + "$"
			regex, err := regexp.Compile(regexPattern)
			if err != nil {
				return fmt.Errorf("白名单正则表达式编译失败: %s, %v", pattern, err)
			}
			p.whiteListRegex = append(p.whiteListRegex, regex)
		} else {
			// 精确匹配
			p.whiteListExact[pattern] = true
		}
	}
	return nil
}

// isWhiteListed 检查路径是否在白名单中
func (p *Plugin) isWhiteListed(path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// 精确匹配
	if p.whiteListExact[path] {
		return true
	}

	// 正则匹配
	for _, regex := range p.whiteListRegex {
		if regex.MatchString(path) {
			return true
		}
	}

	return false
}

// getTokenFromAuthorizationHeader 从Authorization头获取token
func (p *Plugin) getTokenFromAuthorizationHeader(ctx *gin.Context) string {
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if strings.HasPrefix(authHeader, bearerPrefix) {
		return strings.TrimPrefix(authHeader, bearerPrefix)
	}

	return ""
}

// callAuthService 调用外部认证服务
func (p *Plugin) callAuthService(ctx *gin.Context, token string) error {
	// 构建认证URL
	authURL := fmt.Sprintf("http://%s%s/%s", p.config.Consumers.Host, p.config.Consumers.AuthAPI, token)

	// 创建请求
	req, err := http.NewRequest("GET", authURL, nil)
	if err != nil {
		ctx.JSON(ErrAuthServiceCallFailed.Code, ErrAuthServiceCallFailed)
		ctx.Abort()
		return fmt.Errorf("创建认证请求失败: %v", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timeout", "1000")

	// 发送请求
	resp, err := p.httpClient.Do(req)
	if err != nil {
		ctx.JSON(ErrAuthServiceCallFailed.Code, ErrAuthServiceCallFailed)
		ctx.Abort()
		return fmt.Errorf("调用认证服务失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		errResp := ErrorResponse{
			Code:    resp.StatusCode,
			Message: "Unauthorized: Invalid response",
		}
		ctx.JSON(resp.StatusCode, errResp)
		ctx.Abort()
		return fmt.Errorf("认证服务返回错误状态码: %d", resp.StatusCode)
	}

	// 读取响应体
	var body []byte
	if resp.Body != nil {
		body = make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		body = body[:n]
	}

	if len(body) == 0 {
		ctx.JSON(ErrNoResponseBody.Code, ErrNoResponseBody)
		ctx.Abort()
		return fmt.Errorf("认证服务返回空响应体")
	}

	// 解析响应
	responseBody := strings.TrimSpace(string(body))
	switch responseBody {
	case "true":
		// 认证失败
		ctx.JSON(ErrForbiddenAccessDenied.Code, ErrForbiddenAccessDenied)
		ctx.Abort()
		return fmt.Errorf("认证失败")
	case "false":
		// 认证成功，继续处理
		return nil
	default:
		// 未知响应
		ctx.JSON(ErrUnknownResponseType.Code, ErrUnknownResponseType)
		ctx.Abort()
		return fmt.Errorf("认证服务返回未知响应: %s", responseBody)
	}
}

// Stop 停止插件
func (p *Plugin) Stop() error {
	// 清理资源
	p.mu.Lock()
	defer p.mu.Unlock()

	p.whiteListRegex = nil
	p.whiteListExact = make(map[string]bool)

	return nil
}
