package router

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gateway-go/internal/config"
	"gateway-go/internal/netutil"
	"gateway-go/internal/plugin"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ErrRouteNotFound 未找到匹配路由错误
var ErrRouteNotFound = errors.New("未找到匹配的路由规则")

// RouteMatchType 路由匹配类型
type RouteMatchType string

const (
	MatchExact    RouteMatchType = "exact"    // 精确匹配
	MatchPrefix   RouteMatchType = "prefix"   // 前缀匹配
	MatchRegex    RouteMatchType = "regex"    // 正则匹配
	MatchWildcard RouteMatchType = "wildcard" // 通配符匹配
)

// RouteMatch 路由匹配规则
type RouteMatch struct {
	Type        RouteMatchType    `yaml:"type" json:"type"`
	Path        string            `yaml:"path" json:"path"`
	Regex       string            `yaml:"regex" json:"regex"`
	Host        string            `yaml:"host" json:"host"`
	Method      string            `yaml:"method" json:"method"`
	Headers     map[string]string `yaml:"headers" json:"headers"`
	QueryParams map[string]string `yaml:"query_params" json:"query_params"`
	Weight      int               `yaml:"weight" json:"weight"`
	Priority    int               `yaml:"priority" json:"priority"`
	Namespace   string            `yaml:"namespace" json:"namespace"`
	ABTest      *ABTestConfig     `yaml:"ab_test" json:"ab_test"`
}

// ABTestConfig A/B测试配置
type ABTestConfig struct {
	Enabled      bool    `yaml:"enabled" json:"enabled"`
	GroupA       float64 `yaml:"group_a_percentage" json:"group_a_percentage"`
	GroupB       float64 `yaml:"group_b_percentage" json:"group_b_percentage"`
	GroupATarget string  `yaml:"group_a_target" json:"group_a_target"`
	GroupBTarget string  `yaml:"group_b_target" json:"group_b_target"`
}

// RouteDefinition 路由定义 (包含目标配置与响应配置)
type RouteDefinition struct {
	Name     string                 `yaml:"name" json:"name"`
	Match    RouteMatch             `yaml:"match" json:"match"`
	Target   TargetService          `yaml:"target" json:"target"`
	Plugins  []string               `yaml:"plugins" json:"plugins"`
	Response *config.ResponseConfig `yaml:"response" json:"response"`
}

// RouterConfig 路由配置
type RouterConfig struct {
	Routes []RouteDefinition `yaml:"routes" json:"routes"`
}

// Manager 路由管理器 (REQ-P1-03 单一事实来源)
// 职责分离：仅负责高效路由匹配，不执行插件链
type Manager struct {
	config        *RouterConfig
	configPath    string
	mu            sync.RWMutex
	watcher       *fsnotify.Watcher
	pluginManager *plugin.Manager // 保持字段兼容，但 MatchRoute 中不再执行
	regexCache    map[string]*regexp.Regexp
	configManager *config.ConfigManager
	configCenter  *config.ConfigCenter // 保持向后兼容

	trieRouter *TrieRouter // Trie 树路由器
	routeCache *RouteCache // 路由缓存 (带 LRU 与多维 key)
}

// NewManager 从文件创建路由管理器
func NewManager(configPath string, pluginManager *plugin.Manager) (*Manager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监视器失败: %w", err)
	}

	m := &Manager{
		configPath:    configPath,
		watcher:       watcher,
		pluginManager: pluginManager,
		regexCache:    make(map[string]*regexp.Regexp),
		routeCache:    NewRouteCache(DefaultMaxCacheEntries),
	}

	if err := m.loadConfig(); err != nil {
		_ = watcher.Close()
		return nil, err
	}

	// 启动配置热加载
	go m.watchConfig()

	return m, nil
}

// NewManagerFromConfig 从配置管理器创建路由管理器
func NewManagerFromConfig(configManager *config.ConfigManager, pluginManager *plugin.Manager) *Manager {
	m := &Manager{
		configManager: configManager,
		pluginManager: pluginManager,
		regexCache:    make(map[string]*regexp.Regexp),
		routeCache:    NewRouteCache(DefaultMaxCacheEntries),
	}

	_ = m.loadConfigFromManager()
	return m
}

// NewManagerWithConfig 直接基于 Config 创建路由管理器 (供快照原子构建使用)
func NewManagerWithConfig(cfg *config.Config) *Manager {
	m := &Manager{
		regexCache: make(map[string]*regexp.Regexp),
		routeCache: NewRouteCache(DefaultMaxCacheEntries),
	}

	if cfg != nil {
		m.applyConfigRoutes(cfg.Routes)
	} else {
		m.applyConfigRoutes(nil)
	}

	return m
}

// NewManagerWithRoutes 直接基于路由切片创建路由管理器 (供测试使用)
func NewManagerWithRoutes(routes []RouteDefinition) *Manager {
	m := &Manager{
		regexCache: make(map[string]*regexp.Regexp),
		routeCache: NewRouteCache(DefaultMaxCacheEntries),
		config: &RouterConfig{
			Routes: routes,
		},
	}

	m.buildTrieAndCache()
	return m
}

// NewManagerFromConfigCenter 从配置中心创建路由管理器（保持向后兼容）
func NewManagerFromConfigCenter(configCenter *config.ConfigCenter, pluginManager *plugin.Manager) *Manager {
	m := &Manager{
		configCenter:  configCenter,
		pluginManager: pluginManager,
		regexCache:    make(map[string]*regexp.Regexp),
		routeCache:    NewRouteCache(DefaultMaxCacheEntries),
	}

	_ = m.loadConfigFromCenter()
	return m
}

// ReloadFromConfig 从配置管理器重新加载路由配置
func (m *Manager) ReloadFromConfig(configManager *config.ConfigManager, pluginManager *plugin.Manager) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configManager = configManager
	m.pluginManager = pluginManager
	return m.loadConfigFromManager()
}

// buildTrieAndCache 根据当前配置构建 Trie 路由器并重置缓存 (调用方需持有写锁)
func (m *Manager) buildTrieAndCache() {
	m.trieRouter = NewTrieRouter()
	if m.config != nil {
		for i := range m.config.Routes {
			r := &m.config.Routes[i]
			// 精确匹配与前缀匹配进入 Trie 树
			if r.Match.Type == MatchExact || r.Match.Type == MatchPrefix || r.Match.Type == "" {
				m.trieRouter.Insert(r.Match.Path, r)
			}
		}
	}
	if m.routeCache == nil {
		m.routeCache = NewRouteCache(DefaultMaxCacheEntries)
	} else {
		m.routeCache.Clear()
	}
}

// applyConfigRoutes 转换并应用 config.RouteConfig 切片
func (m *Manager) applyConfigRoutes(cfgRoutes []config.RouteConfig) {
	routes := make([]RouteDefinition, 0, len(cfgRoutes))
	for _, route := range cfgRoutes {
		routeDef := RouteDefinition{
			Name: route.Name,
			Target: TargetService{
				URL:     route.Target.URL,
				Timeout: route.Target.Timeout,
				Retries: route.Target.Retries,
			},
			Plugins:  route.Plugins,
			Response: route.Response,
			Match: RouteMatch{
				Type:        RouteMatchType(route.Match.Type),
				Path:        route.Match.Path,
				Priority:    route.Match.Priority,
				Host:        route.Match.Host,
				Method:      route.Match.Method,
				Headers:     route.Match.Headers,
				QueryParams: route.Match.QueryParams,
			},
		}
		routes = append(routes, routeDef)
	}

	m.config = &RouterConfig{Routes: routes}
	m.buildTrieAndCache()
}

// loadConfig 加载本地配置文件
func (m *Manager) loadConfig() error {
	v := viper.New()
	v.SetConfigFile(m.configPath)
	v.SetConfigType(filepath.Ext(m.configPath)[1:])

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg RouterConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = &cfg
	m.buildTrieAndCache()
	return nil
}

// loadConfigFromManager 从配置管理器加载配置
func (m *Manager) loadConfigFromManager() error {
	if m.configManager == nil {
		return fmt.Errorf("配置管理器未初始化")
	}

	cfg := m.configManager.GetConfig()
	if cfg == nil {
		return fmt.Errorf("配置未加载")
	}

	m.applyConfigRoutes(cfg.Routes)
	return nil
}

// loadConfigFromCenter 从配置中心加载配置（保持向后兼容）
func (m *Manager) loadConfigFromCenter() error {
	if m.configCenter == nil {
		return fmt.Errorf("配置中心未初始化")
	}

	cfg := m.configCenter.GetCurrentConfig()
	if cfg == nil {
		return fmt.Errorf("配置中心未初始化")
	}

	m.applyConfigRoutes(cfg.Routes)
	return nil
}

// watchConfig 监视配置文件变化
func (m *Manager) watchConfig() {
	if m.watcher == nil {
		return
	}
	if err := m.watcher.Add(filepath.Dir(m.configPath)); err != nil {
		return
	}

	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if event.Name == m.configPath && (event.Op&fsnotify.Write == fsnotify.Write) {
				_ = m.loadConfig()
			}
		case _, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// MatchRoute 统一路由匹配入口 (REQ-P1-03)
// 职责纯粹：匹配最优路由规则，不调用插件链
func (m *Manager) MatchRoute(c *gin.Context) (*RouteDefinition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config == nil || len(m.config.Routes) == 0 {
		return nil, ErrRouteNotFound
	}

	reqMethod := c.Request.Method
	reqHost := c.Request.Host
	reqPath := c.Request.URL.Path

	// 1. 优先查多维键路由缓存
	cacheKey := BuildRouteCacheKey(reqMethod, reqHost, reqPath)
	if m.routeCache != nil {
		if cached, ok := m.routeCache.Get(cacheKey); ok {
			// 校验动态规则 (Headers, QueryParams 等)
			if _, matched := m.scoreRoute(c, cached); matched {
				return m.resolveTarget(c, cached)
			}
		}
	}

	var bestRoute *RouteDefinition
	var highestScore int64 = -1

	// 2. Trie 树查找候选路由（支持精确末节点 + 前缀回溯）
	if m.trieRouter != nil {
		candidates := m.trieRouter.FindCandidateRoutes(reqPath)
		for _, cand := range candidates {
			score, matched := m.scoreRoute(c, cand)
			if matched && score > highestScore {
				highestScore = score
				bestRoute = cand
			}
		}
	}

	// 3. 正则 / 通配符等非纯 Trie 路径路由检查 (以及兜底线性校验)
	for i := range m.config.Routes {
		r := &m.config.Routes[i]
		if r.Match.Type == MatchRegex || r.Match.Type == MatchWildcard {
			if m.matchPath(reqPath, r.Match) {
				score, matched := m.scoreRoute(c, r)
				if matched && score > highestScore {
					highestScore = score
					bestRoute = r
				}
			}
		}
	}

	if bestRoute == nil {
		return nil, ErrRouteNotFound
	}

	// 4. 命中后写入多维键缓存
	if m.routeCache != nil {
		m.routeCache.Set(cacheKey, bestRoute)
	}

	return m.resolveTarget(c, bestRoute)
}

// resolveTarget 解析路由最终目标（如处理 A/B 测试）
func (m *Manager) resolveTarget(c *gin.Context, route *RouteDefinition) (*RouteDefinition, error) {
	if route.Match.ABTest != nil && route.Match.ABTest.Enabled {
		selectedTarget, err := m.handleABTest(c, route)
		if err == nil && selectedTarget != nil {
			cloned := *route
			cloned.Target = *selectedTarget
			return &cloned, nil
		}
	}
	return route, nil
}

// scoreRoute 计算候选路由的匹配得分并判定是否匹配
// 裁决规则 (REQ-P1-03):
// 1. 更具体的 host 优先于泛化 host: 精确 host (300) > 通配符 host (200) > 无 host (100)
// 2. 更精确/更长的路径优先: exact 末节点 (50,000,000) > prefix (按路径长度降序)
// 3. 携带特定 Header / Query 匹配条件的路由优先于通用路由
// 4. 同等特异性下比较 Priority 降序
func (m *Manager) scoreRoute(c *gin.Context, route *RouteDefinition) (int64, bool) {
	rule := route.Match
	reqPath := c.Request.URL.Path

	// 1. Host 匹配与得分
	hostScore := int64(100) // 无 host 时得分为 100
	if rule.Host != "" {
		if !matchHost(c.Request.Host, rule.Host) {
			return 0, false
		}
		if strings.Contains(rule.Host, "*") {
			hostScore = 200 // 通配符 host 得分为 200
		} else {
			hostScore = 300 // 精确 host 得分为 300
		}
	}

	// 2. Method 匹配检查
	if rule.Method != "" {
		if !matchMethod(c.Request.Method, rule.Method) {
			return 0, false
		}
	}

	// 3. Headers 匹配检查
	for key, value := range rule.Headers {
		if c.GetHeader(key) != value {
			return 0, false
		}
	}

	// 4. QueryParams 匹配检查
	for key, value := range rule.QueryParams {
		if c.Query(key) != value {
			return 0, false
		}
	}

	// 5. 路径匹配确认 (针对非 Trie 路由或缓存命中时的校验)
	if !m.matchPath(reqPath, rule) {
		return 0, false
	}

	// 路径特异性加分:
	// 精确匹配完全命中请求路径，获得极大优先权
	exactBonus := int64(0)
	if (rule.Type == MatchExact || string(rule.Type) == "exact") &&
		(reqPath == rule.Path || strings.TrimSuffix(reqPath, "/") == strings.TrimSuffix(rule.Path, "/")) {
		exactBonus = 50_000_000
	}

	// 路径长度加分: 越深的前缀匹配越具体
	pathLenBonus := int64(len(rule.Path)) * 100_000

	// 特异性维度 (Headers/QueryParams) 加分
	conditionBonus := int64(len(rule.Headers)+len(rule.QueryParams)) * 10_000

	// 优先级加分: [0, 1000]
	priorityScore := int64(rule.Priority)

	score := hostScore*10_000_000_000 + exactBonus + pathLenBonus + conditionBonus + priorityScore
	return score, true
}

// matchHost 校验请求 Host 是否匹配规则 Host (支持精确比对、忽略端口与通配符 *.example.com)
func matchHost(reqHost, pattern string) bool {
	if pattern == "" {
		return true
	}

	req := strings.ToLower(strings.TrimSpace(reqHost))
	pat := strings.ToLower(strings.TrimSpace(pattern))

	// 若 pattern 不包含端口，则剔除 req 中的端口进行比较
	if !strings.Contains(pat, ":") {
		if hostOnly, _, err := net.SplitHostPort(req); err == nil {
			req = hostOnly
		}
	}

	if req == pat {
		return true
	}

	// 支持 *.example.com 通配符匹配
	if strings.HasPrefix(pat, "*.") {
		domainSuffix := pat[1:] // ".example.com"
		if strings.HasSuffix(req, domainSuffix) {
			return true
		}
	}

	return false
}

// matchMethod 校验请求方法是否匹配（支持逗号分隔多方法与大小写不敏感）
func matchMethod(reqMethod, ruleMethod string) bool {
	if ruleMethod == "" {
		return true
	}
	methods := strings.Split(ruleMethod, ",")
	req := strings.ToUpper(strings.TrimSpace(reqMethod))
	for _, m := range methods {
		if strings.ToUpper(strings.TrimSpace(m)) == req {
			return true
		}
	}
	return false
}

// matchPath 匹配路径
func (m *Manager) matchPath(path string, rule RouteMatch) bool {
	switch rule.Type {
	case MatchExact:
		return path == rule.Path || (rule.Path != "/" && strings.TrimSuffix(path, "/") == strings.TrimSuffix(rule.Path, "/"))
	case MatchPrefix, "":
		if rule.Path == "/" || rule.Path == "" {
			return true
		}
		if path == rule.Path {
			return true
		}
		if strings.HasPrefix(path, rule.Path) {
			// 确保路径边界正确，例如 /api 匹配 /api/v1 但不匹配 /apikey
			if strings.HasSuffix(rule.Path, "/") || strings.HasPrefix(path[len(rule.Path):], "/") {
				return true
			}
		}
		return false
	case MatchRegex:
		regex, err := m.getRegex(rule.Regex)
		if err != nil {
			return false
		}
		return regex.MatchString(path)
	case MatchWildcard:
		return m.matchWildcard(path, rule.Path)
	default:
		return path == rule.Path
	}
}

// getRegex 获取或编译正则表达式
func (m *Manager) getRegex(pattern string) (*regexp.Regexp, error) {
	m.mu.RLock()
	regex, exists := m.regexCache[pattern]
	m.mu.RUnlock()

	if exists {
		return regex, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if regex, exists = m.regexCache[pattern]; exists {
		return regex, nil
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	m.regexCache[pattern] = compiled
	return compiled, nil
}

// matchWildcard 通配符路径匹配
func (m *Manager) matchWildcard(path, pattern string) bool {
	regexPattern := strings.ReplaceAll(pattern, "*", ".*")
	regexPattern = "^" + regexPattern + "$"

	regex, err := m.getRegex(regexPattern)
	if err != nil {
		return false
	}

	return regex.MatchString(path)
}

// handleABTest 处理 A/B 测试目标选择
func (m *Manager) handleABTest(c *gin.Context, route *RouteDefinition) (*TargetService, error) {
	bucketKey := c.GetHeader("X-Request-ID")
	if bucketKey == "" {
		bucketKey = c.GetHeader("X-User-ID")
	}
	if bucketKey == "" {
		var trustedProxies []string
		if m.configManager != nil {
			if cfg := m.configManager.GetConfig(); cfg != nil {
				trustedProxies = cfg.Server.TrustedProxies
			}
		}
		if len(trustedProxies) == 0 {
			trustedProxies = netutil.GetDefaultTrustedProxies()
		}
		bucketKey = netutil.ClientIP(c, trustedProxies)
	}

	hash := m.hashString(bucketKey)
	percentage := float64(hash%100) / 100.0

	if percentage < route.Match.ABTest.GroupA {
		return &TargetService{
			URL: route.Match.ABTest.GroupATarget,
		}, nil
	} else if percentage < route.Match.ABTest.GroupA+route.Match.ABTest.GroupB {
		return &TargetService{
			URL: route.Match.ABTest.GroupBTarget,
		}, nil
	}

	return &route.Target, nil
}

// hashString 计算字符串哈希值
func (m *Manager) hashString(s string) uint32 {
	var h uint32
	for i := 0; i < len(s); i++ {
		h = h*31 + uint32(s[i])
	}
	return h
}

// Close 关闭路由管理器
func (m *Manager) Close() error {
	if m.watcher != nil {
		return m.watcher.Close()
	}
	return nil
}
