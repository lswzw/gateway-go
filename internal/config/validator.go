package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// ValidateConfig 验证配置
func ValidateConfig(config *Config) error {
	if err := validateServerConfig(&config.Server); err != nil {
		return fmt.Errorf("服务器配置验证失败: %w", err)
	}

	if err := validateLogConfig(&config.Log); err != nil {
		return fmt.Errorf("日志配置验证失败: %w", err)
	}

	if err := validatePluginsConfig(&config.Plugins); err != nil {
		return fmt.Errorf("插件配置验证失败: %w", err)
	}

	if err := validateRoutesConfig(config.Routes); err != nil {
		return fmt.Errorf("路由配置验证失败: %w", err)
	}

	return nil
}

// validateServerConfig 验证服务器配置
func validateServerConfig(config *ServerConfig) error {
	if config.Port <= 0 || config.Port > 65535 {
		return fmt.Errorf("无效的端口号: %d", config.Port)
	}

	if config.ReadTimeout <= 0 {
		return fmt.Errorf("无效的读取超时时间: %v", config.ReadTimeout)
	}

	if config.WriteTimeout <= 0 {
		return fmt.Errorf("无效的写入超时时间: %v", config.WriteTimeout)
	}

	if config.MaxHeaderBytes <= 0 {
		return fmt.Errorf("无效的最大请求头大小: %d", config.MaxHeaderBytes)
	}

	if config.MaxBodyBytes < 0 {
		return fmt.Errorf("无效的最大请求体大小: %d (必须 >= 0)", config.MaxBodyBytes)
	}

	if config.GracefulShutdownTimeout <= 0 {
		return fmt.Errorf("无效的优雅关闭超时时间: %v", config.GracefulShutdownTimeout)
	}

	for _, proxy := range config.TrustedProxies {
		trimmed := strings.TrimSpace(proxy)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			if _, _, err := net.ParseCIDR(trimmed); err != nil {
				return fmt.Errorf("无效的可信代理CIDR: %s (%w)", proxy, err)
			}
		} else {
			if ip := net.ParseIP(trimmed); ip == nil {
				return fmt.Errorf("无效的可信代理IP: %s", proxy)
			}
		}
	}

	return nil
}

// validateLogConfig 验证日志配置
func validateLogConfig(config *LogConfig) error {
	if config.Level == "" {
		return fmt.Errorf("日志级别不能为空")
	}

	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	if !validLevels[config.Level] {
		return fmt.Errorf("无效的日志级别: %s", config.Level)
	}

	if config.Format == "" {
		return fmt.Errorf("日志格式不能为空")
	}

	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}

	if !validFormats[config.Format] {
		return fmt.Errorf("无效的日志格式: %s", config.Format)
	}

	if config.Output == "" {
		return fmt.Errorf("日志输出不能为空")
	}

	if config.MaxSize <= 0 {
		return fmt.Errorf("无效的最大文件大小: %d", config.MaxSize)
	}

	if config.MaxAge <= 0 {
		return fmt.Errorf("无效的文件保留天数: %d", config.MaxAge)
	}

	if config.MaxBackups <= 0 {
		return fmt.Errorf("无效的备份文件数量: %d", config.MaxBackups)
	}

	if config.BodyLimit < 0 || config.BodyLimit > 65536 {
		return fmt.Errorf("无效的请求体记录上限: %d (有效范围 0-65536)", config.BodyLimit)
	}

	return nil
}

// validatePluginsConfig 验证插件配置
func validatePluginsConfig(config *PluginsConfig) error {
	// 验证可用插件
	for i, plugin := range config.Available {
		if err := validatePluginConfig(&plugin); err != nil {
			return fmt.Errorf("可用插件[%d]配置验证失败: %w", i, err)
		}
	}

	// 验证路由插件
	for routeName, plugins := range config.Routes {
		for i, plugin := range plugins {
			if err := validatePluginConfig(&plugin); err != nil {
				return fmt.Errorf("路由[%s]插件[%d]配置验证失败: %w", routeName, i, err)
			}
		}
	}

	return nil
}

// validatePluginConfig 验证单个插件配置
func validatePluginConfig(config *PluginConfig) error {
	if config.Name == "" {
		return fmt.Errorf("插件名称不能为空")
	}

	if config.Name == "error" {
		return fmt.Errorf("error plugin has been deprecated; error handling is now unified into gateway core respondError")
	}

	if config.Order < 0 {
		return fmt.Errorf("无效的插件执行顺序: %d", config.Order)
	}

	return nil
}

// validateRoutesConfig 验证路由配置
func validateRoutesConfig(routes []RouteConfig) error {
	if len(routes) == 0 {
		return fmt.Errorf("至少需要配置一个路由")
	}

	for i, route := range routes {
		if err := validateRouteConfig(&route); err != nil {
			return fmt.Errorf("路由[%d]配置验证失败: %w", i, err)
		}
	}

	return nil
}

// validateRouteConfig 验证单个路由配置
func validateRouteConfig(config *RouteConfig) error {
	if config.Name == "" {
		return fmt.Errorf("路由名称不能为空")
	}

	if config.Match.Priority < 0 {
		return fmt.Errorf("无效的路由优先级: %d", config.Match.Priority)
	}

	matchType := config.Match.Type
	if matchType == "" {
		matchType = "prefix"
	}

	validTypes := map[string]bool{
		"exact":    true,
		"prefix":   true,
		"regex":    true,
		"wildcard": true,
	}
	if !validTypes[matchType] {
		return fmt.Errorf("路由[%s]不支持的匹配类型: %s", config.Name, config.Match.Type)
	}

	// 路径及正则/通配符校验
	switch matchType {
	case "regex":
		pattern := config.Match.Regex
		if pattern == "" {
			pattern = config.Match.Path
		}
		if pattern == "" {
			return fmt.Errorf("路由[%s]正则匹配类型缺少正则表达式", config.Name)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("路由[%s]无效的正则表达式[%s]: %w", config.Name, pattern, err)
		}
	case "wildcard":
		if config.Match.Path == "" {
			return fmt.Errorf("路由[%s]通配符匹配类型缺少路径", config.Name)
		}
		regexPattern := GlobToRegex(config.Match.Path)
		if _, err := regexp.Compile(regexPattern); err != nil {
			return fmt.Errorf("路由[%s]无效的通配符模式[%s]: %w", config.Name, config.Match.Path, err)
		}
	default:
		if config.Match.Path == "" {
			return fmt.Errorf("路由[%s]路径不能为空", config.Name)
		}
	}

	// A/B测试校验
	if config.Match.ABTest != nil && config.Match.ABTest.Enabled {
		ab := config.Match.ABTest
		if ab.GroupATarget == "" && ab.GroupBTarget == "" {
			return fmt.Errorf("路由[%s]启用了A/B测试但未配置目标URL", config.Name)
		}
		if ab.GroupATarget != "" && !strings.HasPrefix(ab.GroupATarget, "internal://") {
			if _, err := url.Parse(ab.GroupATarget); err != nil {
				return fmt.Errorf("路由[%s]A/B测试GroupA目标URL无效: %w", config.Name, err)
			}
		}
		if ab.GroupBTarget != "" && !strings.HasPrefix(ab.GroupBTarget, "internal://") {
			if _, err := url.Parse(ab.GroupBTarget); err != nil {
				return fmt.Errorf("路由[%s]A/B测试GroupB目标URL无效: %w", config.Name, err)
			}
		}
		if ab.WeightA < 0 || ab.WeightB < 0 {
			return fmt.Errorf("路由[%s]A/B测试权重不能为负数", config.Name)
		}
		if ab.GroupA < 0 || ab.GroupB < 0 {
			return fmt.Errorf("路由[%s]A/B测试百分比不能为负数", config.Name)
		}
	}

	if config.Target.URL == "" {
		return fmt.Errorf("路由目标URL不能为空")
	}

	// 检查是否为内部URL
	if !strings.HasPrefix(config.Target.URL, "internal://") {
		// 对于非内部URL，验证URL格式
		if _, err := url.Parse(config.Target.URL); err != nil {
			return fmt.Errorf("无效的目标URL: %s", config.Target.URL)
		}
	}

	return nil
}
