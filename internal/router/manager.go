package router

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

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
	Type        RouteMatchType    `yaml:"type"`
	Path        string            `yaml:"path"`
	Regex       string            `yaml:"regex"`
	Host        string            `yaml:"host"`
	Method      string            `yaml:"method"`
	Headers     map[string]string `yaml:"headers"`
	QueryParams map[string]string `yaml:"query_params"`
	Weight      int               `yaml:"weight"`
	Priority    int               `yaml:"priority"`
	Namespace   string            `yaml:"namespace"`
	ABTest      *ABTestConfig     `yaml:"ab_test"`
}

// ABTestConfig A/B测试配置
type ABTestConfig struct {
	Enabled      bool    `yaml:"enabled"`
	GroupA       float64 `yaml:"group_a_percentage"`
	GroupB       float64 `yaml:"group_b_percentage"`
	GroupATarget string  `yaml:"group_a_target"`
	GroupBTarget string  `yaml:"group_b_target"`
}

// RouteDefinition 路由定义
type RouteDefinition struct {
	Name    string        `yaml:"name"`
	Match   RouteMatch    `yaml:"match"`
	Target  TargetService `yaml:"target"`
	Plugins []string      `yaml:"plugins"`
}

// RouterConfig 路由配置
type RouterConfig struct {
	Routes []RouteDefinition `yaml:"routes"`
}

// Manager 路由管理器
type Manager struct {
	config        *RouterConfig
	configPath    string
	mu            sync.RWMutex
	watcher       *fsnotify.Watcher
	pluginManager *plugin.Manager
	regexCache    map[string]*regexp.Regexp
	configManager *config.ConfigManager
	configCenter  *config.ConfigCenter // 保持向后兼容

	trieRouter *TrieRouter // Trie 路由器
	routeCache *RouteCache // 路由缓存
}

// NewManager 创建路由管理器
func NewManager(configPath string, pluginManager *plugin.Manager) (*Manager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监视器失败: %v", err)
	}

	m := &Manager{
		configPath:    configPath,
		watcher:       watcher,
		pluginManager: pluginManager,
		regexCache:    make(map[string]*regexp.Regexp),
	}

	if err := m.loadConfig(); err != nil {
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
	}

	// 加载初始配置
	m.loadConfigFromManager()

	return m
}

// NewManagerFromConfigCenter 从配置中心创建路由管理器（向后兼容）
func NewManagerFromConfigCenter(configCenter *config.ConfigCenter, pluginManager *plugin.Manager) *Manager {
	m := &Manager{
		configCenter:  configCenter,
		pluginManager: pluginManager,
		regexCache:    make(map[string]*regexp.Regexp),
	}

	// 加载初始配置
	m.loadConfigFromCenter()

	return m
}

// ReloadFromConfig 从配置管理器重新加载配置
func (m *Manager) ReloadFromConfig(configManager *config.ConfigManager, pluginManager *plugin.Manager) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 更新配置管理器引用
	m.configManager = configManager
	m.pluginManager = pluginManager

	// 重新加载配置
	return m.loadConfigFromManager()
}

// loadConfig 加载路由配置
func (m *Manager) loadConfig() error {
	v := viper.New()
	v.SetConfigFile(m.configPath)
	v.SetConfigType(filepath.Ext(m.configPath)[1:]) // 根据文件扩展名设置配置类型

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	var config RouterConfig
	if err := v.Unmarshal(&config); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	m.mu.Lock()
	m.config = &config

	// 构建 Trie 路由树
	m.trieRouter = NewTrieRouter()
	for i := range config.Routes {
		m.trieRouter.Insert(config.Routes[i].Match.Path, &config.Routes[i])
	}

	// 初始化路由缓存
	m.routeCache = NewRouteCache(1024)

	m.mu.Unlock()

	return nil
}

// loadConfigFromManager 从配置管理器加载路由配置
func (m *Manager) loadConfigFromManager() error {
	if m.configManager == nil {
		return fmt.Errorf("配置管理器未初始化")
	}

	cfg := m.configManager.GetConfig()
	if cfg == nil {
		return fmt.Errorf("配置未加载")
	}

	// 转换配置格式
	var routes []RouteDefinition
	for _, route := range cfg.Routes {
		routeDef := RouteDefinition{
			Name: route.Name,
			Target: TargetService{
				URL:     route.Target.URL,
				Timeout: route.Target.Timeout,
				Retries: route.Target.Retries,
			},
			Plugins: route.Plugins,
		}

		// 转换匹配规则
		routeDef.Match = RouteMatch{
			Type:        RouteMatchType(route.Match.Type),
			Path:        route.Match.Path,
			Priority:    route.Match.Priority,
			Host:        route.Match.Host,
			Method:      route.Match.Method,
			Headers:     route.Match.Headers,
			QueryParams: route.Match.QueryParams,
		}

		routes = append(routes, routeDef)
	}

	routerConfig := &RouterConfig{
		Routes: routes,
	}

	m.config = routerConfig
	return nil
}

// loadConfigFromCenter 从配置中心加载路由配置（向后兼容）
func (m *Manager) loadConfigFromCenter() error {
	if m.configCenter == nil {
		return fmt.Errorf("配置中心未初始化")
	}

	cfg := m.configCenter.GetCurrentConfig()
	if cfg == nil {
		return fmt.Errorf("配置中心未初始化")
	}

	// 转换配置格式
	var routes []RouteDefinition
	for _, route := range cfg.Routes {
		routeDef := RouteDefinition{
			Name: route.Name,
			Target: TargetService{
				URL:     route.Target.URL,
				Timeout: route.Target.Timeout,
				Retries: route.Target.Retries,
			},
			Plugins: route.Plugins,
		}

		// 转换匹配规则
		routeDef.Match = RouteMatch{
			Type:        RouteMatchType(route.Match.Type),
			Path:        route.Match.Path,
			Priority:    route.Match.Priority,
			Host:        route.Match.Host,
			Method:      route.Match.Method,
			Headers:     route.Match.Headers,
			QueryParams: route.Match.QueryParams,
		}

		routes = append(routes, routeDef)
	}

	routerConfig := &RouterConfig{
		Routes: routes,
	}

	m.config = routerConfig
	return nil
}

// watchConfig 监视配置文件变化
func (m *Manager) watchConfig() {
	// 添加配置文件到监视列表
	if err := m.watcher.Add(filepath.Dir(m.configPath)); err != nil {
		fmt.Printf("监视配置文件失败: %v\n", err)
		return
	}

	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if event.Name == m.configPath && (event.Op&fsnotify.Write == fsnotify.Write) {
				if err := m.loadConfig(); err != nil {
					fmt.Printf("重新加载配置失败: %v\n", err)
				} else {
					fmt.Println("配置已更新")
				}
			}
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("监视配置文件错误: %v\n", err)
		}
	}
}

// MatchRoute 匹配路由规则
func (m *Manager) MatchRoute(c *gin.Context) (*TargetService, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config == nil {
		return nil, fmt.Errorf("路由配置未加载")
	}

	path := c.Request.URL.Path

	// 1. 优先查缓存
	if m.routeCache != nil {
		if route, ok := m.routeCache.Get(path); ok {
			if m.matchRule(c, route.Match) {
				if err := m.pluginManager.Execute(c, route.Name); err != nil {
					return nil, err
				}
				if route.Match.ABTest != nil && route.Match.ABTest.Enabled {
					return m.handleABTest(c, route)
				}
				return &route.Target, nil
			}
		}
	}

	// 2. Trie 路由查找
	if m.trieRouter != nil {
		if route, ok := m.trieRouter.Match(path); ok {
			if m.matchRule(c, route.Match) {
				// 命中后写入缓存
				if m.routeCache != nil {
					m.routeCache.Set(path, route)
				}
				if err := m.pluginManager.Execute(c, route.Name); err != nil {
					return nil, err
				}
				if route.Match.ABTest != nil && route.Match.ABTest.Enabled {
					return m.handleABTest(c, route)
				}
				return &route.Target, nil
			}
		}
	}

	// 3. 兜底：原有线性遍历
	var bestMatch *RouteDefinition
	highestPriority := -1

	for _, route := range m.config.Routes {
		if !m.matchRule(c, route.Match) {
			continue
		}

		if route.Match.Priority > highestPriority {
			highestPriority = route.Match.Priority
			bestMatch = &route
		}
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("未找到匹配的路由规则")
	}

	// 命中后写入缓存
	if m.routeCache != nil {
		m.routeCache.Set(path, bestMatch)
	}

	if err := m.pluginManager.Execute(c, bestMatch.Name); err != nil {
		return nil, err
	}

	if bestMatch.Match.ABTest != nil && bestMatch.Match.ABTest.Enabled {
		return m.handleABTest(c, bestMatch)
	}

	return &bestMatch.Target, nil
}

// matchRule 匹配规则
func (m *Manager) matchRule(c *gin.Context, rule RouteMatch) bool {
	// 路径匹配
	if !m.matchPath(c.Request.URL.Path, rule) {
		return false
	}

	// 主机匹配
	if rule.Host != "" && c.Request.Host != rule.Host {
		return false
	}

	// 方法匹配
	if rule.Method != "" && c.Request.Method != rule.Method {
		return false
	}

	// 请求头匹配
	for key, value := range rule.Headers {
		if c.GetHeader(key) != value {
			return false
		}
	}

	// 查询参数匹配
	for key, value := range rule.QueryParams {
		if c.Query(key) != value {
			return false
		}
	}

	return true
}

// matchPath 匹配路径
func (m *Manager) matchPath(path string, rule RouteMatch) bool {
	switch rule.Type {
	case MatchExact:
		return path == rule.Path
	case MatchPrefix:
		return strings.HasPrefix(path, rule.Path)
	case MatchRegex:
		regex, err := m.getRegex(rule.Regex)
		if err != nil {
			return false
		}
		return regex.MatchString(path)
	case MatchWildcard:
		return m.matchWildcard(path, rule.Path)
	default:
		return false
	}
}

// getRegex 获取正则表达式
func (m *Manager) getRegex(pattern string) (*regexp.Regexp, error) {
	m.mu.RLock()
	regex, exists := m.regexCache[pattern]
	m.mu.RUnlock()

	if exists {
		return regex, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查
	regex, exists = m.regexCache[pattern]
	if exists {
		return regex, nil
	}

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	m.regexCache[pattern] = regex
	return regex, nil
}

// matchWildcard 通配符匹配
func (m *Manager) matchWildcard(path, pattern string) bool {
	// 将通配符模式转换为正则表达式
	regexPattern := strings.ReplaceAll(pattern, "*", ".*")
	regexPattern = "^" + regexPattern + "$"

	regex, err := m.getRegex(regexPattern)
	if err != nil {
		return false
	}

	return regex.MatchString(path)
}

// handleABTest 处理A/B测试
func (m *Manager) handleABTest(c *gin.Context, route *RouteDefinition) (*TargetService, error) {
	// 使用请求ID或用户ID作为分桶依据
	bucketKey := c.GetHeader("X-Request-ID")
	if bucketKey == "" {
		bucketKey = c.GetHeader("X-User-ID")
	}
	if bucketKey == "" {
		bucketKey = c.ClientIP()
	}

	// 计算哈希值
	hash := m.hashString(bucketKey)
	percentage := float64(hash%100) / 100.0

	// 根据百分比选择目标
	if percentage < route.Match.ABTest.GroupA {
		return &TargetService{
			URL: route.Match.ABTest.GroupATarget,
		}, nil
	} else if percentage < route.Match.ABTest.GroupA+route.Match.ABTest.GroupB {
		return &TargetService{
			URL: route.Match.ABTest.GroupBTarget,
		}, nil
	}

	// 默认返回原始目标
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
	return m.watcher.Close()
}
