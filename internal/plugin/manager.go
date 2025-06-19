package plugin

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"gateway-go/internal/plugin/chain"
	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// Manager 插件管理器
type Manager struct {
	// 可用插件注册表
	availablePlugins map[string]core.Plugin
	// 路由插件映射
	routeChains map[string]*chain.Chain
	// 插件注册表（保持向后兼容）
	registry map[string]core.Plugin
	mu       sync.RWMutex

	pluginCache *PluginCache // 插件结果缓存
}

// NewManager 创建插件管理器
func NewManager() *Manager {
	return &Manager{
		availablePlugins: make(map[string]core.Plugin),
		routeChains:      make(map[string]*chain.Chain),
		registry:         make(map[string]core.Plugin),
		pluginCache:      NewPluginCache(10 * time.Second), // 默认10秒，可调整
	}
}

// Register 注册插件
func (m *Manager) Register(p core.Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := p.Name()
	if _, exists := m.registry[name]; exists {
		return fmt.Errorf("插件 %s 已注册", name)
	}

	m.registry[name] = p
	return nil
}

// RegisterAvailablePlugin 注册可用插件
func (m *Manager) RegisterAvailablePlugin(name string, p core.Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.availablePlugins[name]; exists {
		return fmt.Errorf("可用插件 %s 已注册", name)
	}

	m.availablePlugins[name] = p
	return nil
}

// LoadAvailablePlugins 加载可用插件配置
func (m *Manager) LoadAvailablePlugins(configs []PluginConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 按执行顺序排序
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Order < configs[j].Order
	})

	// 加载可用插件
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		p, exists := m.registry[cfg.Name]
		if !exists {
			return fmt.Errorf("插件 %s 未注册", cfg.Name)
		}

		if err := p.Init(cfg.Config); err != nil {
			return fmt.Errorf("初始化插件 %s 失败: %v", cfg.Name, err)
		}

		// 注册为可用插件
		m.availablePlugins[cfg.Name] = p
	}

	return nil
}

// LoadRoutePlugins 加载路由插件
func (m *Manager) LoadRoutePlugins(routeName string, pluginNames []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := chain.NewChainWithCache(m.pluginCache, GenerateCacheKey)

	// 根据插件名称加载路由插件
	for _, pluginName := range pluginNames {
		p, exists := m.availablePlugins[pluginName]
		if !exists {
			return fmt.Errorf("路由 %s 使用的插件 %s 不可用", routeName, pluginName)
		}

		ch.AddPlugin(p)
	}

	m.routeChains[routeName] = ch
	return nil
}

// Execute 执行插件链
func (m *Manager) Execute(ctx *gin.Context, routeName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 执行路由插件
	if chain, exists := m.routeChains[routeName]; exists {
		if err := chain.Execute(ctx); err != nil {
			return err
		}
	}

	return nil
}
