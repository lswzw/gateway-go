package interface_auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

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
		Code:    http.StatusUnauthorized,
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
	Host     string `yaml:"host" json:"host"`
	AuthAPI  string `yaml:"auth_api" json:"auth_api"`
	AuthMode string `yaml:"auth_mode" json:"auth_mode"` // "http_status" | "legacy_unauthorized_bool"
	CacheTTL int    `yaml:"cache_ttl" json:"cache_ttl"` // 秒数，默认 0 不缓存
}

// Config 插件配置结构体
type Config struct {
	WhiteInterfaces []string        `yaml:"white_interfaces" json:"white_interfaces"`
	Consumers       ConsumersConfig `yaml:"consumers" json:"consumers"`
	CacheTTL        int             `yaml:"cache_ttl" json:"cache_ttl"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		WhiteInterfaces: []string{},
		Consumers: ConsumersConfig{
			AuthMode: "http_status",
			CacheTTL: 0,
		},
		CacheTTL: 0,
	}
}

// authCacheEntry 认证结果缓存项
type authCacheEntry struct {
	passed    bool
	errStatus int
	errMsg    string
	expiredAt time.Time
}

// Plugin 接口认证插件
type Plugin struct {
	*core.BasePlugin
	config         *Config
	whiteListRegex []*regexp.Regexp
	whiteListExact map[string]bool
	httpClient     *http.Client
	mu             sync.RWMutex
	cache          map[string]authCacheEntry
	cacheMu        sync.RWMutex
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
		cache: make(map[string]authCacheEntry),
	}
}

// SetHTTPClient 设置内部 HTTP Client（主要用于测试注入 mock client）
func (p *Plugin) SetHTTPClient(client *http.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.httpClient = client
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
	if p.config.Consumers.AuthMode == "" {
		p.config.Consumers.AuthMode = "http_status"
	}
	if err := p.compileWhiteList(); err != nil {
		return fmt.Errorf("白名单正则编译失败: %v", err)
	}
	return nil
}

// getAuthMode 获取认证模式
func (p *Plugin) getAuthMode() string {
	mode := strings.ToLower(strings.TrimSpace(p.config.Consumers.AuthMode))
	if mode == "legacy_unauthorized_bool" {
		return "legacy_unauthorized_bool"
	}
	return "http_status"
}

// getCacheTTL 获取缓存 TTL（秒）
func (p *Plugin) getCacheTTL() int {
	if p.config.Consumers.CacheTTL > 0 {
		return p.config.Consumers.CacheTTL
	}
	if p.config.CacheTTL > 0 {
		return p.config.CacheTTL
	}
	return 0
}

// setCache 写入认证缓存
func (p *Plugin) setCache(token string, passed bool, errStatus int, errMsg string) {
	ttl := p.getCacheTTL()
	if ttl <= 0 {
		return
	}
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	// 缓存容量保护，防止内存泄露
	if len(p.cache) >= 10000 {
		now := time.Now()
		for k, v := range p.cache {
			if now.After(v.expiredAt) {
				delete(p.cache, k)
			}
		}
	}

	p.cache[token] = authCacheEntry{
		passed:    passed,
		errStatus: errStatus,
		errMsg:    errMsg,
		expiredAt: time.Now().Add(time.Duration(ttl) * time.Second),
	}
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
		response.RespondError(ctx, http.StatusUnauthorized, "Token missing or invalid", response.GetRequestID(ctx))
		return nil
	}

	// 检查本地缓存
	if p.getCacheTTL() > 0 {
		p.cacheMu.RLock()
		entry, found := p.cache[token]
		p.cacheMu.RUnlock()
		if found && time.Now().Before(entry.expiredAt) {
			if entry.passed {
				ctx.Set("plugin_result_interface_auth", "cached_success")
				return nil
			}
			response.RespondError(ctx, entry.errStatus, entry.errMsg, response.GetRequestID(ctx))
			return nil
		}
	}

	// 调用外部认证服务
	if err := p.callAuthService(ctx, token); err != nil {
		if !ctx.Writer.Written() {
			response.RespondError(ctx, http.StatusUnauthorized, err.Error(), response.GetRequestID(ctx))
		}
		return nil
	}
	if ctx.IsAborted() || ctx.Writer.Written() {
		return nil
	}

	// 认证成功，写入标记
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
	baseURL := strings.TrimSpace(p.config.Consumers.Host)
	if baseURL == "" {
		response.RespondError(ctx, http.StatusInternalServerError, "Auth service host not configured", response.GetRequestID(ctx))
		return nil
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	authAPI := p.config.Consumers.AuthAPI
	if !strings.HasPrefix(authAPI, "/") {
		authAPI = "/" + authAPI
	}
	// 严禁将 Token 拼接到 URL 路径！
	authURL := strings.TrimRight(baseURL, "/") + authAPI

	// 创建请求
	req, err := http.NewRequestWithContext(ctx.Request.Context(), "GET", authURL, nil)
	if err != nil {
		response.RespondError(ctx, http.StatusInternalServerError, "Auth service call failed", response.GetRequestID(ctx))
		return nil
	}

	// 设置请求头：Token 统一由请求头 Authorization: Bearer <token> 传递
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timeout", "1000")

	// 发送请求
	p.mu.RLock()
	client := p.httpClient
	p.mu.RUnlock()

	resp, err := client.Do(req)
	if err != nil {
		response.RespondError(ctx, http.StatusInternalServerError, "Auth service call failed", response.GetRequestID(ctx))
		return nil
	}
	defer resp.Body.Close()

	// 响应体读取上限 4KB (io.LimitReader)
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		response.RespondError(ctx, http.StatusInternalServerError, "Auth service call failed", response.GetRequestID(ctx))
		return nil
	}

	authMode := p.getAuthMode()
	if authMode == "legacy_unauthorized_bool" {
		if len(bodyBytes) == 0 {
			response.RespondError(ctx, http.StatusInternalServerError, "Internal error: no response body", response.GetRequestID(ctx))
			return nil
		}
		responseBody := strings.TrimSpace(string(bodyBytes))
		switch responseBody {
		case "false":
			// 历史 PaaS 接口兼容模式：响应体 "false" 判定为通过
			p.setCache(token, true, 0, "")
			return nil
		case "true":
			// 响应体 "true" 判定为认证失败
			p.setCache(token, false, http.StatusUnauthorized, "Forbidden: Access denied")
			response.RespondError(ctx, http.StatusUnauthorized, "Forbidden: Access denied", response.GetRequestID(ctx))
			return nil
		default:
			// 未知响应
			p.setCache(token, false, http.StatusUnauthorized, "response Unknown Type")
			response.RespondError(ctx, http.StatusUnauthorized, "response Unknown Type", response.GetRequestID(ctx))
			return nil
		}
	}

	// 标准 http_status 模式：2xx 为通过，401/403 为拒绝
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.setCache(token, true, 0, "")
		return nil
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		p.setCache(token, false, http.StatusUnauthorized, "Forbidden: Access denied")
		response.RespondError(ctx, http.StatusUnauthorized, "Forbidden: Access denied", response.GetRequestID(ctx))
		return nil
	}

	// 其它状态码视为认证服务调用失败
	response.RespondError(ctx, http.StatusInternalServerError, "Auth service call failed", response.GetRequestID(ctx))
	return nil
}

// Stop 停止插件并清理资源
func (p *Plugin) Stop() error {
	p.mu.Lock()
	p.whiteListRegex = nil
	p.whiteListExact = make(map[string]bool)
	p.mu.Unlock()

	p.cacheMu.Lock()
	p.cache = make(map[string]authCacheEntry)
	p.cacheMu.Unlock()

	return nil
}
