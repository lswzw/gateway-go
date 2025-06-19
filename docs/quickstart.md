# 快速开始指南

## 概述

Gateway-Go 是一个基于 Go 语言开发的高性能 API 网关服务，支持动态配置、路由转发和插件化扩展功能。本指南将帮助您在 5 分钟内快速上手。

## 功能特点

- 🚀 **高性能**：基于 Gin 框架，支持高并发处理
- 🔧 **动态配置**：支持配置文件热重载，无需重启服务
- 🔌 **灵活路由**：支持多种路由匹配模式（精确、前缀、正则、通配符）
- 🔗 **插件化架构**：支持插件化扩展，精准控制每个路由的插件
- 🔒 **安全认证**：内置认证、限流、熔断等安全插件
- 📊 **监控完善**：集成日志、错误处理等监控插件
- 🔄 **高可用**：支持熔断、重试、降级等容错机制

## 环境要求

- Go 1.16 或更高版本
- 依赖包：
  - github.com/gin-gonic/gin
  - github.com/spf13/viper
  - github.com/fsnotify/fsnotify

## 快速安装

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

### 4. 运行服务

```bash
./bin/gateway
```

或者直接运行：

```bash
go run cmd/gateway/main.go
```

服务默认在 8080 端口启动。

## 基础配置

### 配置文件结构

配置文件位于 `config/config.yaml`：

```yaml
# 服务器配置
server:
  port: 8080

# 插件配置
plugins:
  available:
    - name: auth
      enabled: true
      order: 1
      config:
        type: token
        token_header: Authorization
    - name: rate_limit
      enabled: true
      order: 2
      config:
        requests_per_second: 100

# 路由配置
routes:
  - name: api-service
    match:
      type: prefix
      path: /api
    target:
      url: http://localhost:8081
    plugins: ["auth", "rate_limit"]  # 指定该路由使用的插件
```

### 配置说明

- **server**: 服务器基本配置

## 快速测试

### 1. 健康检查

```bash
curl http://localhost:8080/health
```

应该返回：

```json
{
  "status": "ok"
}
```

### 2. API 请求（需要认证）

```bash
curl -H "Authorization: Bearer your-token" http://localhost:8080/api/users
```

### 3. 配置管理

```bash
# 获取当前配置
curl http://localhost:8080/admin/config/current

# 重新加载配置
curl -X POST http://localhost:8080/admin/config/reload

# 测试配置
curl -X POST http://localhost:8080/admin/config/test
```

## 插件系统

网关采用插件化架构，支持精准的插件控制：

### 可用插件

- **认证插件 (auth)**：支持 Token 和 Basic 认证
- **限流插件 (rate_limit)**：支持基于 IP 和用户的限流
- **熔断器插件 (circuit_breaker)**：保护后端服务
- **跨域插件 (cors)**：处理跨域请求
- **日志插件 (logger)**：请求日志记录
- **错误处理插件 (error)**：统一错误处理
- **IP白名单插件 (ip_whitelist)**：IP访问控制
- **一致性校验插件 (consistency)**：请求签名验证

### 插件配置特点

- **精准控制**：插件默认不生效，只在路由中明确配置时才生效
- **灵活组合**：每个路由可以配置不同的插件组合
- **性能优化**：只加载路由实际需要的插件
- **配置清晰**：插件配置和路由配置分离

## 动态配置

服务支持配置文件的动态加载，当您修改 `config/config.yaml` 文件时：

1. 服务会自动检测到配置文件的变化
2. 重新加载配置和插件
3. 重新注册所有路由
4. 无需重启服务即可使新配置生效

## 下一步

- 📖 查看 [详细配置说明](configuration.md)
- 🔌 了解 [插件系统](plugins/reference.md)
- 🏗️ 学习 [架构设计](architecture.md)
- 🚀 参考 [部署指南](deployment.md)

## 常见问题

### Q: 配置文件修改后不生效？
A: 确保配置文件格式正确，服务会自动检测变化并重新加载。

### Q: 插件如何启用？
A: 插件需要在 `plugins.available` 中定义，并在路由的 `plugins` 字段中指定使用。

### Q: 如何添加新的路由？
A: 在 `routes` 配置中添加新的路由规则，指定匹配条件和目标服务。

### Q: 服务启动失败？
A: 检查配置文件格式、端口占用情况和依赖包是否正确安装。 