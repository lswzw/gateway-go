package registry

import (
	"fmt"
	"sync"

	"gateway-go/internal/plugin/core"
)

// Registry 插件注册表
type Registry struct {
	plugins map[string]core.Plugin
	mu      sync.RWMutex
}

// NewRegistry 创建插件注册表
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]core.Plugin),
	}
}

// Register 注册插件
func (r *Registry) Register(p core.Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("插件 %s 已注册", name)
	}

	r.plugins[name] = p
	return nil
}

// Get 获取插件
func (r *Registry) Get(name string) (core.Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, exists := r.plugins[name]
	return p, exists
}

// List 列出所有插件
func (r *Registry) List() []core.Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]core.Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}
