package plugin

import (
	"fmt"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"
)

// PluginState 插件状态
type PluginState int

const (
	StateStopped PluginState = iota
	StateStarting
	StateRunning
	StateStopping
	StateFailed
)

// PluginInfo 插件信息
type PluginInfo struct {
	Plugin       core.Plugin
	State        PluginState
	StartTime    time.Time
	LastError    error
	Dependencies []string
	Config       interface{}
}

// LifecycleManager 插件生命周期管理器
type LifecycleManager struct {
	plugins   map[string]*PluginInfo
	mu        sync.RWMutex
	stateChan chan *PluginStateChange
	stopChan  chan struct{}
}

// PluginStateChange 插件状态变更
type PluginStateChange struct {
	Name      string
	OldState  PluginState
	NewState  PluginState
	Error     error
	Timestamp time.Time
}

// NewLifecycleManager 创建生命周期管理器
func NewLifecycleManager() *LifecycleManager {
	return &LifecycleManager{
		plugins:   make(map[string]*PluginInfo),
		stateChan: make(chan *PluginStateChange, 100),
		stopChan:  make(chan struct{}),
	}
}

// Register 注册插件
func (m *LifecycleManager) Register(p core.Plugin, config interface{}, dependencies []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := p.Name()
	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("插件 %s 已注册", name)
	}

	m.plugins[name] = &PluginInfo{
		Plugin:       p,
		State:        StateStopped,
		Dependencies: dependencies,
		Config:       config,
	}

	return nil
}

// Start 启动插件
func (m *LifecycleManager) Start(name string) error {
	m.mu.Lock()
	info, exists := m.plugins[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("插件 %s 未注册", name)
	}

	if info.State != StateStopped {
		m.mu.Unlock()
		return fmt.Errorf("插件 %s 状态错误: %v", name, info.State)
	}

	// 检查依赖
	for _, dep := range info.Dependencies {
		depInfo, exists := m.plugins[dep]
		if !exists {
			m.mu.Unlock()
			return fmt.Errorf("插件 %s 依赖的插件 %s 未注册", name, dep)
		}
		if depInfo.State != StateRunning {
			m.mu.Unlock()
			return fmt.Errorf("插件 %s 依赖的插件 %s 未运行", name, dep)
		}
	}

	info.State = StateStarting
	m.mu.Unlock()

	// 初始化插件
	if err := info.Plugin.Init(info.Config); err != nil {
		m.updateState(name, StateFailed, err)
		return fmt.Errorf("初始化插件 %s 失败: %v", name, err)
	}

	m.updateState(name, StateRunning, nil)
	return nil
}

// Stop 停止插件
func (m *LifecycleManager) Stop(name string) error {
	m.mu.Lock()
	info, exists := m.plugins[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("插件 %s 未注册", name)
	}

	if info.State != StateRunning {
		m.mu.Unlock()
		return fmt.Errorf("插件 %s 状态错误: %v", name, info.State)
	}

	// 检查是否有其他插件依赖此插件
	for _, p := range m.plugins {
		for _, dep := range p.Dependencies {
			if dep == name && p.State == StateRunning {
				m.mu.Unlock()
				return fmt.Errorf("插件 %s 被其他运行中的插件依赖", name)
			}
		}
	}

	info.State = StateStopping
	m.mu.Unlock()

	// 停止插件
	if stopPlugin, ok := info.Plugin.(interface{ Stop() error }); ok {
		if err := stopPlugin.Stop(); err != nil {
			m.updateState(name, StateFailed, err)
			return fmt.Errorf("停止插件 %s 失败: %v", name, err)
		}
	}

	m.updateState(name, StateStopped, nil)
	return nil
}

// UpdateConfig 更新插件配置
func (m *LifecycleManager) UpdateConfig(name string, config interface{}) error {
	m.mu.Lock()
	info, exists := m.plugins[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("插件 %s 未注册", name)
	}

	oldConfig := info.Config
	info.Config = config
	m.mu.Unlock()

	// 如果插件正在运行，需要重启以应用新配置
	if info.State == StateRunning {
		if err := m.Stop(name); err != nil {
			info.Config = oldConfig
			return err
		}
		return m.Start(name)
	}

	return nil
}

// GetState 获取插件状态
func (m *LifecycleManager) GetState(name string) (PluginState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.plugins[name]
	if !exists {
		return StateStopped, fmt.Errorf("插件 %s 未注册", name)
	}

	return info.State, nil
}

// GetPlugin 获取插件实例
func (m *LifecycleManager) GetPlugin(name string) (core.Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("插件 %s 未注册", name)
	}

	return info.Plugin, nil
}

// ListPlugins 列出所有插件
func (m *LifecycleManager) ListPlugins() map[string]*PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugins := make(map[string]*PluginInfo)
	for name, info := range m.plugins {
		plugins[name] = info
	}

	return plugins
}

// updateState 更新插件状态
func (m *LifecycleManager) updateState(name string, state PluginState, err error) {
	m.mu.Lock()
	info, exists := m.plugins[name]
	if !exists {
		m.mu.Unlock()
		return
	}

	oldState := info.State
	info.State = state
	info.LastError = err
	if state == StateRunning {
		info.StartTime = time.Now()
	}
	m.mu.Unlock()

	// 发送状态变更通知
	select {
	case m.stateChan <- &PluginStateChange{
		Name:      name,
		OldState:  oldState,
		NewState:  state,
		Error:     err,
		Timestamp: time.Now(),
	}:
	default:
		// 通道已满，丢弃通知
	}
}

// WatchState 监听插件状态变更
func (m *LifecycleManager) WatchState() <-chan *PluginStateChange {
	return m.stateChan
}

// Close 关闭生命周期管理器
func (m *LifecycleManager) Close() {
	close(m.stopChan)
	close(m.stateChan)
}
