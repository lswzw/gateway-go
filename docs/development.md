# Gateway-Go 开发文档

## 开发环境要求

### 系统要求
- **操作系统**: Linux, macOS, Windows
- **Go 版本**: 1.22 或更高版本
- **内存**: 至少 2GB 可用内存
- **磁盘空间**: 至少 1GB 可用空间

### 依赖工具
- **Git**: 版本控制
- **Make**: 构建工具（可选）
- **Docker**: 容器化部署（可选）

## 项目结构

```
gateway-go/
├── cmd/                    # 应用程序入口
│   └── gateway/
│       └── main.go        # 主程序入口
├── config/                 # 配置文件
│   └── config.yaml        # 主配置文件
├── docs/                   # 文档目录
│   ├── api.md             # API 文档
│   ├── architecture.md    # 架构文档
│   ├── best-practices.md  # 最佳实践
│   ├── deployment.md      # 部署文档
│   └── development.md     # 开发文档
├── internal/               # 内部包
│   ├── config/            # 配置管理
│   │   ├── config.go      # 配置结构定义
│   │   ├── loader.go      # 配置加载器
│   │   ├── validator.go   # 配置验证器
│   │   ├── manager.go     # 配置管理器
│   │   └── center.go      # 配置中心
│   ├── errors/            # 错误处理
│   │   ├── errors.go      # 错误定义
│   │   ├── fallback.go    # 降级处理
│   │   ├── retry.go       # 重试机制
│   │   ├── notifier.go    # 错误通知
│   │   └── i18n.go        # 国际化
│   ├── logger/            # 日志系统
│   │   └── logger.go      # 日志管理器
│   ├── plugin/            # 插件系统
│   │   ├── core/          # 插件核心
│   │   │   └── interface.go # 插件接口
│   │   ├── manager.go     # 插件管理器
│   │   ├── lifecycle.go   # 插件生命周期
│   │   ├── config.go      # 插件配置
│   │   ├── chain/         # 插件链
│   │   │   └── chain.go   # 插件链执行器
│   │   ├── registry/      # 插件注册
│   │   │   └── registry.go # 插件注册器
│   │   └── plugins/       # 内置插件
│   │       ├── auth/      # 认证插件
│   │       ├── ratelimit/ # 限流插件
│   │       ├── circuitbreaker/ # 熔断器插件
│   │       ├── cors/      # 跨域插件
│   │       ├── logger/    # 日志插件
│   │       ├── error/     # 错误处理插件
│   │       ├── ipwhitelist/ # IP白名单插件
│   │       └── consistency/ # 一致性校验插件
│   └── router/            # 路由系统
│       ├── router.go      # 路由管理器
│       ├── manager.go     # 路由管理器
│       └── types.go       # 路由类型定义
├── go.mod                 # Go 模块定义
├── go.sum                 # 依赖校验和
└── README.md              # 项目说明
```

## 快速开始

### 1. 克隆项目

```bash
git clone <repository-url>
cd gateway-go
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 构建项目

```bash
go build -o bin/gateway cmd/gateway/main.go
```

### 4. 运行项目

```bash
./bin/gateway
```

或者直接运行：

```bash
go run cmd/gateway/main.go
```

### 5. 验证运行

```bash
curl http://localhost:8080/health
```

应该返回：

```json
{
  "status": "ok"
}
```

## 开发指南

### 1. 添加新的中间件

#### 步骤 1: 创建中间件目录

```bash
mkdir -p internal/middleware/handlers/yourmiddleware
```

#### 步骤 2: 实现中间件

创建 `internal/middleware/handlers/yourmiddleware/yourmiddleware.go`：

```go
package yourmiddleware

import (
    "github.com/gin-gonic/gin"
)

type YourMiddlewareConfig struct {
    Enabled bool   `yaml:"enabled"`
    Option  string `yaml:"option"`
}

type YourMiddleware struct {
    config YourMiddlewareConfig
}

func NewYourMiddleware(config YourMiddlewareConfig) *YourMiddleware {
    return &YourMiddleware{
        config: config,
    }
}

func (m *YourMiddleware) Handle() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 中间件逻辑
        if !m.config.Enabled {
            c.Next()
            return
        }

        // 前置处理
        // ...

        c.Next()

        // 后置处理
        // ...
    }
}
```

#### 步骤 3: 注册中间件

在 `internal/middleware/factory.go` 中添加：

```go
func (f *Factory) CreateMiddlewares() []gin.HandlerFunc {
    var middlewares []gin.HandlerFunc

    // 添加你的中间件
    if f.config.Middleware.YourMiddleware.Enabled {
        yourMiddleware := yourmiddleware.NewYourMiddleware(f.config.Middleware.YourMiddleware)
        middlewares = append(middlewares, yourMiddleware.Handle())
    }

    return middlewares
}
```

#### 步骤 4: 更新配置结构

在 `internal/config/config.go` 中添加配置结构：

```go
type MiddlewareConfig struct {
    // ... 现有配置
    YourMiddleware YourMiddlewareConfig `yaml:"your_middleware"`
}
```

### 2. 添加新的插件

#### 步骤 1: 创建插件目录

```bash
mkdir -p internal/plugin/plugins/yourplugin
```

#### 步骤 2: 实现插件

创建 `internal/plugin/plugins/yourplugin/yourplugin.go`：

```go
package yourplugin

import (
    "gateway-go/internal/plugin/core"
    "github.com/gin-gonic/gin"
)

type YourPlugin struct {
    config map[string]interface{}
}

func New() *YourPlugin {
    return &YourPlugin{}
}

func (p *YourPlugin) Name() string {
    return "your_plugin"
}

func (p *YourPlugin) Init(config map[string]interface{}) error {
    p.config = config
    return nil
}

func (p *YourPlugin) Execute(ctx *gin.Context) error {
    // 插件逻辑
    return nil
}

func (p *YourPlugin) Order() int {
    return 100
}

func (p *YourPlugin) Stop() error {
    return nil
}

func (p *YourPlugin) GetDependencies() []string {
    return []string{}
}
```

#### 步骤 3: 注册插件

在主程序中注册插件：

```go
func init() {
    // ... 其他初始化代码
    
    // 注册插件
    pluginManager.Register(yourplugin.New())
}
```

### 3. 添加新的路由匹配类型

#### 步骤 1: 更新路由类型

在 `internal/router/types.go` 中添加新的匹配类型：

```go
const (
    // ... 现有类型
    MatchCustom RouteMatchType = "custom"
)
```

#### 步骤 2: 实现匹配逻辑

在 `internal/router/manager.go` 的 `matchPath` 方法中添加：

```go
func (m *Manager) matchPath(path string, rule RouteMatch) bool {
    switch rule.Type {
    // ... 现有匹配逻辑
    case MatchCustom:
        return m.matchCustom(path, rule)
    default:
        return false
    }
}

func (m *Manager) matchCustom(path string, rule RouteMatch) bool {
    // 自定义匹配逻辑
    return false
}
```

## 测试指南

### 1. 运行单元测试

```bash
go test ./...
```

### 2. 运行特定包的测试

```bash
go test ./internal/config
go test ./internal/router
go test ./internal/plugin
```

### 3. 运行基准测试

```bash
go test -bench=. ./internal/router
```

### 4. 生成测试覆盖率报告

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## 调试指南

### 1. 启用调试模式

在配置文件中设置：

```yaml
server:
  mode: debug
```

### 2. 查看详细日志

```yaml
log:
  level: debug
  format: json
```

### 3. 使用 pprof 进行性能分析

```bash
# 启动 pprof 服务器
go tool pprof http://localhost:8080/debug/pprof/profile

# 查看内存使用
go tool pprof http://localhost:8080/debug/pprof/heap
```

### 4. 使用 Delve 调试器

```bash
# 安装 Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# 启动调试
dlv debug cmd/gateway/main.go
```

## 代码规范

### 1. 命名规范

- **包名**: 使用小写字母，避免下划线
- **文件名**: 使用小写字母和下划线
- **函数名**: 使用驼峰命名法
- **常量**: 使用大写字母和下划线
- **变量**: 使用驼峰命名法

### 2. 注释规范

- 所有导出的函数和类型必须有注释
- 使用 `//` 进行行注释
- 使用 `/* */` 进行块注释
- 注释应该说明功能和用途

### 3. 错误处理

- 始终检查错误返回值
- 使用 `fmt.Errorf` 包装错误
- 提供有意义的错误信息

### 4. 接口设计

- 接口应该小而专注
- 遵循单一职责原则
- 提供合理的默认实现

## 性能优化

### 1. 内存优化

- 使用对象池减少 GC 压力
- 避免不必要的内存分配
- 合理使用切片和映射

### 2. 并发优化

- 使用 goroutine 处理并发请求
- 合理使用锁和通道
- 避免 goroutine 泄漏

### 3. 网络优化

- 使用连接池
- 实现请求超时
- 支持压缩和缓存

## 常见问题

### 1. 配置热更新不生效

- 检查配置文件权限
- 确认文件监视器正常工作
- 查看日志中的错误信息

### 2. 插件加载失败

- 检查插件配置格式
- 确认插件已正确注册
- 查看插件初始化日志

### 3. 路由匹配失败

- 检查路由配置格式
- 确认匹配规则正确
- 查看路由匹配日志

### 4. 性能问题

- 使用 pprof 分析性能瓶颈
- 检查内存使用情况
- 优化热点代码

## 贡献指南

### 1. 提交代码

- 创建功能分支
- 编写测试用例
- 更新相关文档
- 提交 Pull Request

### 2. 代码审查

- 确保代码符合规范
- 添加必要的测试
- 更新相关文档
- 检查性能影响

### 3. 发布流程

- 更新版本号
- 生成变更日志
- 创建发布标签
- 部署到生产环境 