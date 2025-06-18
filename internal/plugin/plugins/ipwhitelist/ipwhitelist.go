package ipwhitelist

import (
	"fmt"
	"net"
	"sync"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// IPWhitelistPlugin IP 白名单插件
type IPWhitelistPlugin struct {
	*core.BasePlugin
	config map[string]interface{}
	// IP 白名单
	ipWhitelist sync.Map
}

// New 创建 IP 白名单插件
func New() *IPWhitelistPlugin {
	return &IPWhitelistPlugin{
		BasePlugin: core.NewBasePlugin("ip_whitelist", 5, nil),
	}
}

// Init 初始化插件
func (p *IPWhitelistPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap

	// 初始化 IP 白名单
	if ipWhitelist, ok := configMap["ip_whitelist"].([]interface{}); ok {
		for _, ip := range ipWhitelist {
			if ipStr, ok := ip.(string); ok {
				p.ipWhitelist.Store(ipStr, true)
			}
		}
	}

	return nil
}

// Execute 执行插件
func (p *IPWhitelistPlugin) Execute(ctx *gin.Context) error {
	// 获取客户端 IP
	clientIP := ctx.ClientIP()

	// 检查 IP 是否在白名单中
	if !p.isIPAllowed(clientIP) {
		ctx.JSON(403, gin.H{
			"error": "IP 不在白名单中",
		})
		return fmt.Errorf("IP %s 不在白名单中", clientIP)
	}

	return nil
}

// isIPAllowed 检查 IP 是否允许访问
func (p *IPWhitelistPlugin) isIPAllowed(ip string) bool {
	// 如果白名单为空，则允许所有 IP
	if p.isEmpty() {
		return true
	}

	// 检查精确匹配
	if _, exists := p.ipWhitelist.Load(ip); exists {
		return true
	}

	// 检查 CIDR 匹配
	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		return false
	}

	var allowed bool
	p.ipWhitelist.Range(func(key, value interface{}) bool {
		if cidr, ok := key.(string); ok {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err == nil && ipNet.Contains(clientIP) {
				allowed = true
				return false
			}
		}
		return true
	})

	return allowed
}

// isEmpty 检查白名单是否为空
func (p *IPWhitelistPlugin) isEmpty() bool {
	empty := true
	p.ipWhitelist.Range(func(_, _ interface{}) bool {
		empty = false
		return false
	})
	return empty
}

// AddIP 添加 IP 到白名单
func (p *IPWhitelistPlugin) AddIP(ip string) {
	p.ipWhitelist.Store(ip, true)
}

// RemoveIP 从白名单中移除 IP
func (p *IPWhitelistPlugin) RemoveIP(ip string) {
	p.ipWhitelist.Delete(ip)
}

// GetWhitelist 获取白名单列表
func (p *IPWhitelistPlugin) GetWhitelist() []string {
	var whitelist []string
	p.ipWhitelist.Range(func(key, _ interface{}) bool {
		if ip, ok := key.(string); ok {
			whitelist = append(whitelist, ip)
		}
		return true
	})
	return whitelist
}
