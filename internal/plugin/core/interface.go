package core

import (
	"github.com/gin-gonic/gin"
)

// Plugin 插件接口
//
// 插件错误契约（Plugin Error Contract / REQ-P0-03）：
//  1. 若插件已直接向客户端写出响应（例如鉴权失败 401、白名单拦截 403、限流 429、熔断 503 等），
//     插件必须调用 c.Abort() 并返回 nil，严禁在写出响应后返回 error。
//  2. 若插件未写出响应且希望网关统一返回错误，插件应当返回 error 且不得向 c.Writer 写入任何内容。
//     网关核心中间件将捕获该 error，并统一写出 500 结构化错误响应。
//  3. 网关核心中间件在插件链执行完毕后检查 c.Writer.Written()：
//     - 若 c.Writer.Written() 为 true（包括违规同时写响应与返回 error 的插件），
//       网关忽略返回的 error（记录 warning 日志并调用 c.Abort()），严禁二次写出 500；
//     - 仅当 !c.Writer.Written() 且 error != nil 时，网关输出兜底统一 500 结构化错误响应。
type Plugin interface {
	// Name 返回插件名称
	Name() string
	// Init 初始化插件
	Init(config interface{}) error
	// Execute 执行插件
	//
	// 契约规则：
	// - 插件若写出响应（如 401/403/429/503），必须调用 c.Abort() 并返回 nil；
	// - 插件若未写响应并希望网关报错，返回 error，由网关兜底输出统一 500 JSON。
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
