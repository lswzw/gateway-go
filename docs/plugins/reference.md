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

### 4. 错误处理插件（error）[已彻底下线/Deprecated]
- **说明**: `error` 插件已彻底下线（REQ-P0-04）。错误处理、结构化响应输出与监控告警已全量收敛至网关核心统一错误出口 `respondError`，不再保留独立的 `error` 插件。
- **状态**: 废弃并移除。配置中若存在 `error` 插件将在启动时报错并提示迁移。所有 404/500/502 等错误均由网关核心统一输出标准 `{code, message, request_id}` 格式。

### 5. IP白名单插件（ip_whitelist）
- **文档位置**: `internal/plugin/plugins/ipwhitelist/README.md`
- **功能**: 基于客户端IP地址控制访问权限，支持白名单和黑名单两种模式

### 6. 一致性校验插件（consistency）
- **文档位置**: `internal/plugin/plugins/consistency/README.md`
- **功能**: 对请求进行签名验证，确保数据完整性和防止重放攻击。
- **安全加固 (REQ-P1-05)**:
  - 签名比对使用 `hmac.Equal` 常量时间比较，防御时序攻击；
  - 非对称算法（RSA、ECDSA、Ed25519）采用真实公钥验签客户端 `X-Signature`（严禁伪验签）；
  - 禁用不安全的 MD5 算法；
  - Nonce 存储具备容量上限控制（`max_nonces`，默认 10000）并配置独立后台 ticker 周期性自动清理过期记录，杜绝内存无界膨胀与重放攻击。

### 7. 外部接口认证插件（interface_auth）
- **文档位置**: `internal/plugin/plugins/interface_auth/README.md`
- **功能**: 对请求调用外部认证鉴权服务进行验证，支持路由与全局保护。
- **协议加固 (REQ-P1-05)**:
  - **标准协议改造**：Token 统一从 `Authorization: Bearer <token>` 请求头传递至外部认证服务（严禁 URL 路径拼接）。认证结果默认依据 HTTP 状态码裁决（2xx=通过，401/403=拒绝）；
  - **旧接口兼容配置**：支持配置 `consumers.auth_mode: http_status | legacy_unauthorized_bool`（默认 `http_status`）。当设为 `legacy_unauthorized_bool` 时，支持针对历史 PaaS 接口读取响应体 `"false"` 判为通过，同时仍强制使用 Header 传递 Token；
  - **安全防护与性能**：外部认证服务响应体读取上限为 4KB（`io.LimitReader`），防御恶意膨胀；支持本地认证结果缓存 TTL（`cache_ttl`，秒，默认 0 不缓存）。

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