package config

import (
	"fmt"
	"net/url"
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

	if config.Mode != "debug" && config.Mode != "release" {
		return fmt.Errorf("无效的运行模式: %s", config.Mode)
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

	if config.GracefulShutdownTimeout <= 0 {
		return fmt.Errorf("无效的优雅关闭超时时间: %v", config.GracefulShutdownTimeout)
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

	if config.Match.Path == "" {
		return fmt.Errorf("路由路径不能为空")
	}

	if config.Target.URL == "" {
		return fmt.Errorf("路由目标URL不能为空")
	}

	if _, err := url.Parse(config.Target.URL); err != nil {
		return fmt.Errorf("无效的目标URL: %s", config.Target.URL)
	}

	return nil
}
