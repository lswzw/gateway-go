package chain

import (
	"sort"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// PluginCacheIface 插件缓存接口，避免循环依赖
type PluginCacheIface interface {
	Get(key string) (result interface{}, ok bool)
	Set(key string, result interface{})
}

// GenerateCacheKeyFunc 生成缓存 key 的函数类型
type GenerateCacheKeyFunc func(ctx *gin.Context, pluginName string) string

// Chain 插件链
type Chain struct {
	plugins []core.Plugin
	cache   PluginCacheIface // 插件结果缓存接口
	genKey  GenerateCacheKeyFunc
}

// NewChain 创建插件链
func NewChain() *Chain {
	return &Chain{
		plugins: make([]core.Plugin, 0),
		cache:   nil,
		genKey:  nil,
	}
}

// NewChainWithCache 创建带缓存的插件链
func NewChainWithCache(cache PluginCacheIface, genKey GenerateCacheKeyFunc) *Chain {
	return &Chain{
		plugins: make([]core.Plugin, 0),
		cache:   cache,
		genKey:  genKey,
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
		var cacheKey string
		if c.cache != nil && c.genKey != nil {
			cacheKey = c.genKey(ctx, p.Name())
			if result, ok := c.cache.Get(cacheKey); ok {
				// 命中缓存，回填 ctx
				if dataMap, ok := result.(map[string]interface{}); ok {
					for k, v := range dataMap {
						ctx.Set(k, v)
					}
				}
				continue
			}
		}
		if err := p.Execute(ctx); err != nil {
			return err
		}
		// 执行后写入缓存
		if c.cache != nil && c.genKey != nil {
			resultKey := "plugin_result_" + p.Name()
			if val, exists := ctx.Get(resultKey); exists {
				c.cache.Set(cacheKey, map[string]interface{}{resultKey: val})
			}
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
