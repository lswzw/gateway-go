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

// PluginFactory 插件工厂函数类型 (REQ-P1-02)
// 为后续「同插件多配置 / 多实例（如按路由独立配置不同限流桶）」留扩展点。
// 当前阶段默认返回插件单例或新实例，并在 LoadAvailablePlugins 中支持按工厂构造。
type PluginFactory func() core.Plugin

// Manager 插件管理器
type Manager struct {
	// 可用插件注册表
	availablePlugins map[string]core.Plugin
	// 路由插件映射
	routeChains map[string]*chain.Chain
	// 插件单例注册表（向后兼容）
	registry map[string]core.Plugin
	// 插件工厂注册表（name -> factory 扩展点，REQ-P1-02）
	factories map[string]PluginFactory
	mu        sync.RWMutex
	stopOnce  sync.Once

	pluginCache *PluginCache // 插件结果缓存
}

// NewManager 创建插件管理器
func NewManager() *Manager {
	return &Manager{
		availablePlugins: make(map[string]core.Plugin),
		routeChains:      make(map[string]*chain.Chain),
		registry:         make(map[string]core.Plugin),
		factories:        make(map[string]PluginFactory),
		pluginCache:      NewPluginCache(10 * time.Second), // 默认10秒，可调整
	}
}

// Register 注册插件单例实例
func (m *Manager) Register(p core.Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := p.Name()
	if _, exists := m.registry[name]; exists {
		return fmt.Errorf("插件 %s 已注册", name)
	}

	m.registry[name] = p
	m.factories[name] = func() core.Plugin {
		return p
	}
	return nil
}

// RegisterFactory 注册插件工厂（为同插件多配置/多实例提供扩展点，REQ-P1-02）
func (m *Manager) RegisterFactory(name string, factory PluginFactory) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.factories[name]; exists {
		return fmt.Errorf("插件工厂 %s 已注册", name)
	}

	m.factories[name] = factory
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

// UnregisterAvailablePlugin 从可用插件列表中摘除指定插件并调用其 Stop() (REQ-P1-02)
func (m *Manager) UnregisterAvailablePlugin(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.availablePlugins[name]
	if !exists {
		return nil
	}

	delete(m.availablePlugins, name)
	if p != nil {
		return p.Stop()
	}
	return nil
}

// GetAvailablePlugin 获取指定可用插件
func (m *Manager) GetAvailablePlugin(name string) (core.Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.availablePlugins[name]
	return p, exists
}

// GetAvailablePlugins 获取所有当前可用插件的快照副本
func (m *Manager) GetAvailablePlugins() map[string]core.Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]core.Plugin, len(m.availablePlugins))
	for k, v := range m.availablePlugins {
		res[k] = v
	}
	return res
}

// LoadAvailablePlugins 加载可用插件配置 (REQ-P1-02)
// 治理逻辑：
// 1. 优先校验已下线的 error 插件；
// 2. 对于从 available 中被移除或禁用的插件，将其从 availablePlugins 摘除并调用其 Stop()；
// 3. 对新启用或保持启用的插件调用 Init()，若失败返回错误。
func (m *Manager) LoadAvailablePlugins(configs []PluginConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否有已弃用的插件（优先校验，即使 enabled 为 false 也提示废弃）
	for _, cfg := range configs {
		if cfg.Name == "error" {
			return fmt.Errorf("error plugin has been deprecated; error handling is now unified into gateway core respondError")
		}
	}

	// 统计新配置中启用的插件集合
	newEnabled := make(map[string]PluginConfig)
	for _, cfg := range configs {
		if cfg.Enabled {
			newEnabled[cfg.Name] = cfg
		}
	}

	// 1. 摘除已移除/禁用的插件并调用其 Stop() (REQ-P1-02)
	for name, p := range m.availablePlugins {
		if _, ok := newEnabled[name]; !ok {
			delete(m.availablePlugins, name)
			if p != nil {
				_ = p.Stop()
			}
		}
	}

	// 按执行顺序排序
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Order < configs[j].Order
	})

	// 2. 加载与初始化新启用的可用插件
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		p, exists := m.registry[cfg.Name]
		if !exists {
			if factory, fExists := m.factories[cfg.Name]; fExists {
				p = factory()
				m.registry[cfg.Name] = p
			} else {
				return fmt.Errorf("插件 %s 未注册", cfg.Name)
			}
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
// 若引用的插件不存在于可用插件中，则返回错误以供触发配置回滚 (REQ-P1-02)
func (m *Manager) LoadRoutePlugins(routeName string, pluginNames []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := chain.NewChainWithCache(m.pluginCache, GenerateCacheKey)

	// 根据插件名称加载路由插件
	for _, pluginName := range pluginNames {
		if pluginName == "error" {
			return fmt.Errorf("error plugin has been deprecated; error handling is now unified into gateway core respondError")
		}

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

// StopAll 停止所有可用插件（幂等操作，sync.Once 保护 REQ-P1-02）
func (m *Manager) StopAll() error {
	var errs []error
	m.stopOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		for name, p := range m.availablePlugins {
			if p != nil {
				if err := p.Stop(); err != nil {
					errs = append(errs, fmt.Errorf("停止插件 %s 失败: %w", name, err))
				}
			}
		}

		if m.pluginCache != nil {
			m.pluginCache.Stop()
		}
	})
	if len(errs) > 0 {
		return fmt.Errorf("停止插件时发生错误: %v", errs)
	}
	return nil
}
