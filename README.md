# Go API 网关服务

这是一个基于 Go 语言开发的高性能 API 网关服务，支持动态配置、路由转发和插件化扩展功能。

## 项目架构图

下图展示了 Go API 网关服务的核心架构组件及其交互关系：

```mermaid
flowchart TD
    A["客户端（Client）"] -->|HTTP 请求| B["API 网关服务（Gin HTTP Server）"]
    B --> C["中间件系统\n（日志、错误处理等）"]
    C --> D["路由管理器\n（路由匹配/转发）"]
    D --> E["插件系统\n（认证、限流、熔断、跨域等）"]
    E --> F["目标服务（上游服务）"]
    B <-->|配置变更通知| G["配置中心\n（热更新/版本管理）"]
    G -->|配置下发| B
    B --> H["监控系统\n（Prometheus/Tracing）"]
    B --> I["错误处理系统"]
    style A fill:#f9f,stroke:#333,stroke-width:1px
    style F fill:#bbf,stroke:#333,stroke-width:1px
    style G fill:#bfb,stroke:#333,stroke-width:1px
    style H fill:#ffb,stroke:#333,stroke-width:1px
    style I fill:#fbb,stroke:#333,stroke-width:1px
    style B fill:#fff,stroke:#333,stroke-width:2px
    style C fill:#fff,stroke:#333,stroke-width:1px
    style D fill:#fff,stroke:#333,stroke-width:1px
    style E fill:#fff,stroke:#333,stroke-width:1px
    linkStyle 1,2,3,4 stroke:#888,stroke-width:1px,stroke-dasharray: 5 5
```

> **说明**：
> - 客户端请求首先进入 API 网关服务，由 Gin HTTP Server 负责接收。
> - 全局中间件（如日志、错误处理）先行处理请求。
> - 路由管理器根据配置进行路由匹配和转发。
> - 插件系统按需执行认证、限流、熔断、跨域等插件。
> - 最终请求被转发到目标上游服务。
> - 配置中心支持热更新，变更会实时通知网关服务。
> - 网关服务集成监控与错误处理系统，保障可观测性与稳定性。

## 功能特点

- 🚀 **高性能**：基于 Gin 框架，支持高并发处理
- 🔧 **动态配置**：支持配置文件热重载，无需重启服务
- 🛣️ **灵活路由**：支持多种路由匹配模式（精确、前缀、正则、通配符）
- 🔌 **插件化架构**：支持插件化扩展，精准控制每个路由的插件
- 🔒 **安全防护**：内置限流、熔断等安全插件
- 🔄 **高可用**：支持熔断、重试、降级等容错机制

## 📚 文档

- **[文档中心](docs/README.md)** - 完整的文档索引
- **[快速开始](docs/quickstart.md)** - 5分钟快速上手
- **[架构设计](docs/architecture.md)** - 系统架构和设计理念
- **[配置管理](docs/configuration.md)** - 配置文件详解和最佳实践
- **[路由系统](docs/routing.md)** - 路由匹配和转发机制
- **[插件系统](docs/plugins/reference.md)** - 内置插件详细说明
- **[插件开发](docs/plugins/development.md)** - 自定义插件开发指南
- **[部署指南](docs/deployment.md)** - 详细的部署说明
- **[开发指南](docs/development.md)** - 开发环境搭建和代码规范
- **[最佳实践](docs/best-practices.md)** - 生产环境最佳实践

## 快速开始

### 环境要求

- Go 1.16 或更高版本
- 依赖包：
  - github.com/gin-gonic/gin
  - github.com/spf13/viper
  - github.com/fsnotify/fsnotify

### 安装

```bash
go mod download
```

### 配置说明

配置文件位于 `config/config.yaml`，支持插件化配置：

```yaml
# 服务器配置
server:
  port: 8080
  mode: debug

# 插件配置
plugins:
  available:
    - name: rate_limit
      enabled: true
      order: 1
      config:
        requests_per_second: 100
    - name: circuit_breaker
      enabled: true
      order: 2
      config:
        failure_threshold: 5

# 路由配置
routes:
  - name: api-service
    match:
      type: prefix
      path: /api
    target:
      url: http://localhost:8081
    plugins: ["rate_limit", "circuit_breaker"]  # 指定该路由使用的插件
```

### 运行服务

```bash
go run cmd/gateway/main.go
```

服务默认在 8080 端口启动。

## 插件系统

网关采用插件化架构，支持精准的插件控制：

### 可用插件

- **接口认证插件 (interface_auth)**：支持白名单和外部认证服务
- **错误处理插件 (error)**：统一错误响应格式
- **跨域插件 (cors)**：处理跨域请求
- **熔断器插件 (circuitbreaker)**：保护后端服务
- **IP白名单插件 (ipwhitelist)**：基于IP地址的访问控制
- **一致性校验插件 (consistency)**：验证请求的完整性
- **限流插件 (ratelimit)**：控制请求频率

### 插件配置特点

- **精准控制**：插件默认不生效，只在路由中明确配置时才生效
- **灵活组合**：每个路由可以配置不同的插件组合
- **性能优化**：只加载路由实际需要的插件
- **配置清晰**：插件配置和路由配置分离

详细配置说明请参考 [插件系统文档](docs/plugins/reference.md)。

## 动态配置

服务支持配置文件的动态加载，当您修改 `config/config.yaml` 文件时：

1. 服务会自动检测到配置文件的变化
2. 重新加载配置和插件
3. 重新注册所有路由
4. 无需重启服务即可使新配置生效

## 示例

### 健康检查

```bash
curl http://localhost:8080/gatewaygo/health
```

### 配置管理

网关支持通过命令行参数进行配置管理：

```bash
# 重新加载配置
./gateway-go -s reload

# 优雅停止服务
./gateway-go -s stop

# 立即退出服务
./gateway-go -s quit

# 测试配置文件语法
./gateway-go -t

# 显示帮助信息
./gateway-go -h

# 显示版本信息
./gateway-go -v
```

## 注意事项

1. 确保配置文件格式正确，否则可能导致配置加载失败
2. 插件配置需要先在 `available` 中定义，才能在路由中使用
3. 修改配置文件后，需要执行 `./gateway-go -s reload` 重新加载配置
4. 如果配置加载失败，会在日志中显示错误信息，但不会影响现有路由的运行

## 错误处理

服务会在以下情况记录错误日志：
- 配置文件读取失败
- 配置文件解析失败
- 插件加载失败
- 路由配置错误
- 目标地址解析失败
- 配置重新加载失败

## 贡献

欢迎提交 Issue 和 Pull Request。

## 许可证

MIT License 