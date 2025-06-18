# 插件说明文档列表

本文档列出了所有插件的说明文档位置，每个插件都有独立的详细说明文档。

## 插件说明文档列表

### 1. 限流插件（rate_limit）
- **文档位置**: `internal/plugin/plugins/ratelimit/README.md`
- **功能**: 支持IP、用户、全局等多种限流维度，支持内存和Redis存储

### 2. 熔断器插件（circuit_breaker）
- **文档位置**: `internal/plugin/plugins/circuitbreaker/README.md`
- **功能**: 通过监控请求失败率自动切换服务状态，提升系统稳定性

### 3. 跨域插件（cors）
- **文档位置**: `internal/plugin/plugins/cors/README.md`
- **功能**: 处理跨域资源共享（CORS）请求，支持灵活配置允许的源、方法、头等

### 4. 错误处理插件（error）
- **文档位置**: `internal/plugin/plugins/error/README.md`
- **功能**: 统一处理API错误响应，支持自定义错误页面、响应格式和错误码映射

### 5. IP白名单插件（ip_whitelist）
- **文档位置**: `internal/plugin/plugins/ipwhitelist/README.md`
- **功能**: 基于客户端IP地址控制访问权限，支持白名单和黑名单两种模式

### 6. 一致性校验插件（consistency）
- **文档位置**: `internal/plugin/plugins/consistency/README.md`
- **功能**: 对请求进行签名验证，确保数据完整性和防止重放攻击

## 插件开发指南

如需开发新的插件，请参考以下文档：
- **插件开发文档**: `docs/plugins/development.md`
- **插件架构说明**: `docs/plugins/reference.md`

## 插件配置说明

所有插件都支持以下通用配置：
- `enabled`: 是否启用插件
- `order`: 插件执行顺序
- `config`: 插件具体配置参数

详细的配置参数请参考各插件的独立说明文档。 