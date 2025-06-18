package plugin

// PluginConfig 插件配置
type PluginConfig struct {
	// 插件名称
	Name string `json:"name"`
	// 是否启用
	Enabled bool `json:"enabled"`
	// 执行顺序
	Order int `json:"order"`
	// 插件配置
	Config interface{} `json:"config"`
}
