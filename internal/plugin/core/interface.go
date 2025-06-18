package core

import (
	"github.com/gin-gonic/gin"
)

// Plugin 插件接口
type Plugin interface {
	// Name 返回插件名称
	Name() string
	// Init 初始化插件
	Init(config interface{}) error
	// Execute 执行插件
	Execute(ctx *gin.Context) error
	// Order 返回插件执行顺序
	Order() int
	// Stop 停止插件（可选）
	Stop() error
	// GetDependencies 获取插件依赖（可选）
	GetDependencies() []string
}

// BasePlugin 基础插件实现
type BasePlugin struct {
	name         string
	order        int
	dependencies []string
}

// NewBasePlugin 创建基础插件
func NewBasePlugin(name string, order int, dependencies []string) *BasePlugin {
	return &BasePlugin{
		name:         name,
		order:        order,
		dependencies: dependencies,
	}
}

// Name 返回插件名称
func (p *BasePlugin) Name() string {
	return p.name
}

// Order 返回插件执行顺序
func (p *BasePlugin) Order() int {
	return p.order
}

// Stop 停止插件
func (p *BasePlugin) Stop() error {
	return nil
}

// GetDependencies 获取插件依赖
func (p *BasePlugin) GetDependencies() []string {
	return p.dependencies
}
