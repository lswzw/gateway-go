package interface_auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/cache"
	"gateway-go/internal/logger"
	"gateway-go/internal/metrics"
	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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
	// ErrAuthServiceCallFailed 认证服务不可用时返回 503：
	// 与业务 5xx 区分开，便于告警与上游熔断识别（历史实现返回 500，语义不清）。
	ErrAuthServiceCallFailed = ErrorResponse{
		Code:    http.StatusServiceUnavailable,
		Message: "Auth service call failed",
	}
	ErrNoResponseBody = ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "Internal error: no response body",
	}
	ErrUnknownResponseType = ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: "response Unknown Type",
	}
)

const (
	// DefaultCacheTTL 认证结果本地缓存 TTL（秒）。0 表示关闭缓存。
	// 该值同时是"令牌被撤销后网关仍放行"的时间上界，属于安全旋钮。
	DefaultCacheTTL = 30

	// DefaultCacheMaxEntries 缓存条目上限，应 ≥ 峰值活跃令牌数 × 1.5，
	// 否则 LRU 抖动会让命中率趋零（虽正确但等于没有缓存）。
	DefaultCacheMaxEntries = 20000

	// DefaultAuthTimeout 认证服务单次调用超时
	DefaultAuthTimeout = 5 * time.Second

	// maxAuthBodyBytes 认证响应体读取上限（防超大响应打爆内存）
	maxAuthBodyBytes = 4096

	// failureThreshold 连续失败次数阈值，达到后进入短路窗口
	failureThreshold = 5

	// openDuration 认证服务不可用时的短路窗口
	openDuration = 30 * time.Second

	// pluginResultKey 插件结果写入 gin context 的键
	pluginResultKey = "plugin_result_interface_auth"
)

// ConsumersConfig 认证服务配置
type ConsumersConfig struct {
	Host     string `yaml:"host" json:"host"`
	AuthAPI  string `yaml:"auth_api" json:"auth_api"`
	AuthMode string `yaml:"auth_mode" json:"auth_mode"` // "http_status" | "legacy_unauthorized_bool"
	CacheTTL int    `yaml:"cache_ttl" json:"cache_ttl"` // 秒数，0 表示关闭缓存
}

// Config 插件配置结构体
type Config struct {
	WhiteInterfaces []string        `yaml:"white_interfaces" json:"white_interfaces"`
	Consumers       ConsumersConfig `yaml:"consumers" json:"consumers"`
	CacheTTL        int             `yaml:"cache_ttl" json:"cache_ttl"`
	CacheMaxEntries int             `yaml:"cache_max_entries" json:"cache_max_entries"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		WhiteInterfaces: []string{},
		Consumers: ConsumersConfig{
			AuthMode: "http_status",
			CacheTTL: 0, // 未显式配置 consumers.cache_ttl 时由顶层 CacheTTL 兜底
		},
		CacheTTL:        DefaultCacheTTL,
		CacheMaxEntries: DefaultCacheMaxEntries,
	}
}

// decision 认证结论
type decision int

const (
	decisionAllow decision = iota
	decisionDeny
	decisionUnavailable
)

// verifyResult 一次认证调用的结论
type verifyResult struct {
	decision decision
	status   int
	message  string
}

// Plugin 接口认证插件
//
// 认证结论只缓存在**本地内存**：多副本部署时不跨副本共享，"撤销生效"的时间上界
// 等于 cache_ttl。设计门禁禁止引入外部共享存储（见 internal/cache 包注释）。
type Plugin struct {
	*core.BasePlugin
	config         *Config
	whiteListRegex []*regexp.Regexp
	whiteListExact map[string]bool
	httpClient     *http.Client
	mu             sync.RWMutex
	stopOnce       sync.Once

	resultCache *cache.LRU[bool] // key -> allow
	flight      cache.Group      // 同 token 并发合并，避免冷缓存击穿

	breakerMu sync.Mutex
	failures  int
	openUntil time.Time
}

// New 创建插件实例
func New() *Plugin {
	return &Plugin{
		BasePlugin:     core.NewBasePlugin("interface_auth", 900, nil),
		config:         DefaultConfig(),
		whiteListExact: make(map[string]bool),
		httpClient: &http.Client{
			Timeout: DefaultAuthTimeout,
		},
		resultCache: cache.NewLRU[bool](DefaultCacheMaxEntries),
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
	if p.config.CacheTTL < 0 {
		p.config.CacheTTL = 0
	}
	if p.config.CacheMaxEntries <= 0 {
		p.config.CacheMaxEntries = DefaultCacheMaxEntries
	}
	// 允许热重载调整容量：容量变化时重建缓存，避免旧容量继续生效
	if p.resultCache == nil || p.resultCache.Capacity() != p.config.CacheMaxEntries {
		p.resultCache = cache.NewLRU[bool](p.config.CacheMaxEntries)
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

// getCacheTTL 获取缓存 TTL（秒），0 表示不缓存
func (p *Plugin) getCacheTTL() int {
	if p.config.Consumers.CacheTTL > 0 {
		return p.config.Consumers.CacheTTL
	}
	if p.config.CacheTTL > 0 {
		return p.config.CacheTTL
	}
	return 0
}

// cacheKey 生成缓存键：token 摘要 + 认证服务标识 + 认证模式。
//
// 只哈希令牌而不存明文，避免内存转储/日志泄露凭据；带上 host/auth_api/auth_mode
// 是为了防止同一令牌在不同认证服务/不同规则之间复用结论（历史实现只用 token 作键，
// 多条路由配置不同认证服务时会互相串结论）。
func (p *Plugin) cacheKey(token string) string {
	raw := token + "\x00" + strings.TrimSpace(p.config.Consumers.Host) +
		"\x00" + strings.TrimSpace(p.config.Consumers.AuthAPI) +
		"\x00" + p.getAuthMode()
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// tokenFingerprint 返回令牌指纹，仅用于日志排障
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

// Execute 执行插件
func (p *Plugin) Execute(ctx *gin.Context) error {
	path := ctx.Request.URL.Path

	// 检查是否在白名单中
	if p.isWhiteListed(path) {
		ctx.Set(pluginResultKey, "whitelist")
		return nil
	}

	token := p.getTokenFromAuthorizationHeader(ctx)
	if token == "" {
		response.RespondError(ctx, http.StatusUnauthorized, ErrTokenMissingOrInvalid.Message, response.GetRequestID(ctx))
		return nil
	}

	route := ctx.GetString("route_name")
	key := p.cacheKey(token)
	ttl := p.getCacheTTL()

	// 命中本地缓存：只缓存"放行"结论，拒绝结果不缓存（认证服务抖动不应持续放毒）
	if ttl > 0 {
		if _, ok := p.resultCache.Get(key); ok {
			metrics.RecordAuthCacheHit(route)
			ctx.Set(pluginResultKey, "cached_success")
			return nil
		}
		metrics.RecordAuthCacheMiss(route)
	}

	// 认证服务持续故障时快速失败，避免每个请求都等满 5s 超时把连接与协程占满
	if !p.breakerAllow() {
		metrics.RecordAuthUpstreamCall(route, "open")
		response.RespondError(ctx, http.StatusServiceUnavailable,
			"Auth service temporarily unavailable", response.GetRequestID(ctx))
		return nil
	}

	res := p.verify(key, token, route)
	switch res.decision {
	case decisionAllow:
		if ttl > 0 {
			p.resultCache.Put(key, true, time.Duration(ttl)*time.Second)
		}
		ctx.Set(pluginResultKey, "success")
		return nil
	case decisionDeny:
		ctx.Set(pluginResultKey, "denied")
		response.RespondError(ctx, res.status, res.message, response.GetRequestID(ctx))
		return nil
	default:
		ctx.Set(pluginResultKey, "unavailable")
		response.RespondError(ctx, http.StatusServiceUnavailable, res.message, response.GetRequestID(ctx))
		return nil
	}
}

// verify 校验令牌；同 key 的并发请求合并为一次外呼
func (p *Plugin) verify(key, token, route string) verifyResult {
	v, _, _ := p.flight.Do(key, func() (interface{}, error) {
		return p.doAuth(token, route), nil
	})
	if res, ok := v.(verifyResult); ok {
		return res
	}
	return verifyResult{decision: decisionUnavailable, status: http.StatusServiceUnavailable, message: ErrAuthServiceCallFailed.Message}
}

// doAuth 调用认证服务并归一化结论
//
// 使用脱离请求的 context：合并调用方可能先被取消，不能让等待中的其他请求一起失败。
func (p *Plugin) doAuth(token, route string) verifyResult {
	baseURL := strings.TrimSpace(p.config.Consumers.Host)
	if baseURL == "" {
		return p.unavailable(route, token, "认证服务地址未配置", nil)
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	authAPI := p.config.Consumers.AuthAPI
	if !strings.HasPrefix(authAPI, "/") {
		authAPI = "/" + authAPI
	}
	// 严禁将 Token 拼接到 URL 路径
	authURL := strings.TrimRight(baseURL, "/") + authAPI

	ctx, cancel := context.WithTimeout(context.Background(), DefaultAuthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return p.unavailable(route, token, "构造认证请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timeout", "1000")

	p.mu.RLock()
	client := p.httpClient
	p.mu.RUnlock()
	if client == nil {
		client = &http.Client{Timeout: DefaultAuthTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return p.unavailable(route, token, "认证服务调用失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthBodyBytes))
	if err != nil {
		return p.unavailable(route, token, "读取认证响应失败", err)
	}

	if p.getAuthMode() == "legacy_unauthorized_bool" {
		switch strings.TrimSpace(string(bodyBytes)) {
		case "false":
			// 历史 PaaS 接口兼容模式：响应体 "false" 判定为通过
			p.breakerRecordSuccess()
			metrics.RecordAuthUpstreamCall(route, "ok")
			return verifyResult{decision: decisionAllow}
		case "true":
			p.breakerRecordSuccess()
			metrics.RecordAuthUpstreamCall(route, "deny")
			return verifyResult{decision: decisionDeny, status: http.StatusUnauthorized, message: ErrForbiddenAccessDenied.Message}
		default:
			p.breakerRecordSuccess()
			metrics.RecordAuthUpstreamCall(route, "deny")
			return verifyResult{decision: decisionDeny, status: http.StatusUnauthorized, message: ErrUnknownResponseType.Message}
		}
	}

	// 标准 http_status 模式：2xx 为通过，401/403 为拒绝，其它视为认证服务失败
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.breakerRecordSuccess()
		metrics.RecordAuthUpstreamCall(route, "ok")
		return verifyResult{decision: decisionAllow}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		p.breakerRecordSuccess()
		metrics.RecordAuthUpstreamCall(route, "deny")
		return verifyResult{decision: decisionDeny, status: http.StatusUnauthorized, message: ErrForbiddenAccessDenied.Message}
	}

	return p.unavailable(route, token, "认证服务返回异常状态码", fmt.Errorf("status=%d", resp.StatusCode))
}

// unavailable 构造"认证服务不可用"结论并记账（打点、熔断计数、日志）
func (p *Plugin) unavailable(route, token, msg string, err error) verifyResult {
	p.breakerRecordFailure()
	metrics.RecordAuthUpstreamCall(route, "error")

	if logger.Log != nil {
		fields := []zap.Field{
			zap.String("route_name", route),
			zap.String("token_fp", tokenFingerprint(token)),
			zap.String("auth_host", strings.TrimSpace(p.config.Consumers.Host)),
			zap.String("reason", msg),
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
		}
		logger.Log.Warn("认证服务不可用，返回 503", fields...)
	}

	return verifyResult{decision: decisionUnavailable, status: http.StatusServiceUnavailable, message: ErrAuthServiceCallFailed.Message}
}

// breakerAllow 判断是否允许调用认证服务（短路窗口内直接拒绝）
func (p *Plugin) breakerAllow() bool {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()

	if p.openUntil.IsZero() {
		return true
	}
	if time.Now().Before(p.openUntil) {
		return false
	}
	// 短路窗口结束，重新试探
	p.openUntil = time.Time{}
	p.failures = 0
	return true
}

// breakerRecordFailure 记录一次认证服务失败
func (p *Plugin) breakerRecordFailure() {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()

	p.failures++
	if p.failures >= failureThreshold {
		p.openUntil = time.Now().Add(openDuration)
		p.failures = 0
		if logger.Log != nil {
			logger.Log.Warn("认证服务连续失败，进入短路窗口",
				zap.Duration("window", openDuration))
		}
	}
}

// breakerRecordSuccess 认证服务恢复正常，重置失败计数
func (p *Plugin) breakerRecordSuccess() {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()

	p.failures = 0
	p.openUntil = time.Time{}
}

// compileWhiteList 编译白名单正则表达式
func (p *Plugin) compileWhiteList() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.whiteListRegex = nil
	p.whiteListExact = make(map[string]bool)
	for _, pattern := range p.config.WhiteInterfaces {
		if strings.Contains(pattern, "*") {
			// 通配符转正则：先转义元字符，再把 \* 还原为 .*
			regexPattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
			regex, err := regexp.Compile(regexPattern)
			if err != nil {
				return fmt.Errorf("白名单正则表达式编译失败: %s, %v", pattern, err)
			}
			p.whiteListRegex = append(p.whiteListRegex, regex)
		} else {
			p.whiteListExact[pattern] = true
		}
	}
	return nil
}

// isWhiteListed 检查路径是否在白名单中
func (p *Plugin) isWhiteListed(path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.whiteListExact[path] {
		return true
	}
	for _, regex := range p.whiteListRegex {
		if regex.MatchString(path) {
			return true
		}
	}
	return false
}

// getTokenFromAuthorizationHeader 从 Authorization 头获取 Bearer token
func (p *Plugin) getTokenFromAuthorizationHeader(ctx *gin.Context) string {
	authHeader := ctx.GetHeader("Authorization")
	const bearerPrefix = "Bearer "
	if strings.HasPrefix(authHeader, bearerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
	}
	return ""
}

// CacheSize 返回当前缓存条目数（供测试与排障）
func (p *Plugin) CacheSize() int {
	if p.resultCache == nil {
		return 0
	}
	return p.resultCache.Len()
}

// Stop 停止插件 (sync.Once 保护，重复调用安全不 panic REQ-P1-02)
func (p *Plugin) Stop() error {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.whiteListRegex = nil
		p.whiteListExact = make(map[string]bool)
		p.mu.Unlock()

		if p.resultCache != nil {
			p.resultCache.Purge()
		}
	})
	return nil
}
