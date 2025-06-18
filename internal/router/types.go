package router

// Route 定义路由规则
type Route struct {
	// 路由名称
	Name string `yaml:"name" json:"name"`
	// 匹配规则
	Match MatchRule `yaml:"match" json:"match"`
	// 目标服务
	Target TargetService `yaml:"target" json:"target"`
	// 优先级，数字越大优先级越高
	Priority int `yaml:"priority" json:"priority"`
}

// MatchRule 定义匹配规则
type MatchRule struct {
	// 路径匹配
	Path string `yaml:"path" json:"path"`
	// 主机匹配
	Host string `yaml:"host" json:"host"`
	// 方法匹配
	Method string `yaml:"method" json:"method"`
	// 请求头匹配
	Headers map[string]string `yaml:"headers" json:"headers"`
	// 查询参数匹配
	QueryParams map[string]string `yaml:"query_params" json:"query_params"`
}

// TargetService 定义目标服务
type TargetService struct {
	// 服务地址
	URL string `yaml:"url" json:"url"`
	// 权重（用于负载均衡）
	Weight int `yaml:"weight" json:"weight"`
	// 超时时间（毫秒）
	Timeout int `yaml:"timeout" json:"timeout"`
	// 重试次数
	Retries int `yaml:"retries" json:"retries"`
}

// RouteConfig 路由配置
type RouteConfig struct {
	Routes []Route `yaml:"routes" json:"routes"`
}
