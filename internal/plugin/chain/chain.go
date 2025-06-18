package chain

import (
	"sort"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// Chain 插件链
type Chain struct {
	plugins []core.Plugin
}

// NewChain 创建插件链
func NewChain() *Chain {
	return &Chain{
		plugins: make([]core.Plugin, 0),
	}
}

// AddPlugin 添加插件
func (c *Chain) AddPlugin(p core.Plugin) {
	c.plugins = append(c.plugins, p)
	// 按执行顺序排序
	sort.Slice(c.plugins, func(i, j int) bool {
		return c.plugins[i].Order() < c.plugins[j].Order()
	})
}

// Execute 执行插件链
func (c *Chain) Execute(ctx *gin.Context) error {
	for _, p := range c.plugins {
		if err := p.Execute(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Clear 清空插件链
func (c *Chain) Clear() {
	c.plugins = make([]core.Plugin, 0)
}

// Plugins 获取所有插件
func (c *Chain) Plugins() []core.Plugin {
	return c.plugins
}
