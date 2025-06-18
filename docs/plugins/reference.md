# 内置插件参考

## 概述

Gateway-Go 提供了丰富的内置插件，涵盖认证、限流、熔断、监控、安全等各个方面。所有插件都实现了统一的插件接口，支持灵活的配置和组合使用。

## 插件分类

### 🔐 安全插件
- [认证插件 (auth)](#认证插件-auth)
- [IP白名单插件 (ip_whitelist)](#ip白名单插件-ip_whitelist)
- [一致性校验插件 (consistency)](#一致性校验插件-consistency)

### 🚦 流量控制插件
- [限流插件 (rate_limit)](#限流插件-rate_limit)
- [熔断器插件 (circuit_breaker)](#熔断器插件-circuit_breaker)

### 🌐 网络插件
- [跨域插件 (cors)](#跨域插件-cors)

### 📊 监控插件
- [日志插件 (logger)](#日志插件-logger)
- [错误处理插件 (error)](#错误处理插件-error)

## 认证插件 (auth)

### 功能描述

提供身份验证和授权功能，支持多种认证方式。

### 配置参数

```yaml
- name: auth
  enabled: true
  order: 2
  config:
    type: token                    # 认证类型: token, basic
    token_header: Authorization    # Token请求头名称
    token_prefix: Bearer          # Token前缀
    secret_key: your-secret-key   # JWT密钥
    token_expiry: 3600            # Token过期时间（秒）
    issuer: gateway-go            # Token发行者
    audience: api-users           # Token受众
    algorithms: ["HS256"]         # 支持的算法
    user_claim: sub               # 用户标识字段
    roles_claim: roles            # 角色字段
    required_roles: []            # 必需角色列表
    skip_paths: ["/health"]       # 跳过认证的路径
```

### 认证类型

#### 1. Token认证 (JWT)

```yaml
config:
  type: token
  token_header: Authorization
  token_prefix: Bearer
  secret_key: your-secret-key
  token_expiry: 3600
```

**使用示例**：
```bash
curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." \
     http://localhost:8080/api/users
```

#### 2. Basic认证

```yaml
config:
  type: basic
  users:
    - username: admin
      password: admin123
      roles: ["admin"]
    - username: user
      password: user123
      roles: ["user"]
```

**使用示例**：
```bash
curl -u admin:admin123 http://localhost:8080/api/users
```

### 角色权限控制

```yaml
config:
  type: token
  required_roles: ["admin", "user"]  # 必需的角色
  role_hierarchy:
    admin: ["user", "readonly"]
    user: ["readonly"]
```

### 跳过认证

```yaml
config:
  skip_paths: 
    - "/health"
    - "/metrics"
    - "/api/public/*"
```

## 限流插件 (rate_limit)

### 功能描述

提供请求限流功能，防止系统过载，支持多种限流策略。

### 配置参数

```yaml
- name: rate_limit
  enabled: true
  order: 3
  config:
    requests_per_second: 100      # 每秒请求数限制
    burst: 200                    # 突发请求数限制
    dimension: ip                 # 限流维度: ip, user, global
    storage: memory               # 存储类型: memory, redis
    redis:
      host: localhost
      port: 6379
      password: ""
      db: 0
    window_size: 60               # 时间窗口大小（秒）
    skip_paths: ["/health"]       # 跳过限流的路径
    error_code: 429               # 限流时的HTTP状态码
    error_message: "Too Many Requests"
```

### 限流维度

#### 1. IP限流

```yaml
config:
  dimension: ip
  requests_per_second: 100
  burst: 200
```

#### 2. 用户限流

```yaml
config:
  dimension: user
  requests_per_second: 50
  burst: 100
  user_header: X-User-ID  # 用户标识头
```

#### 3. 全局限流

```yaml
config:
  dimension: global
  requests_per_second: 1000
  burst: 2000
```

### 存储后端

#### 内存存储

```yaml
config:
  storage: memory
  window_size: 60
```

#### Redis存储

```yaml
config:
  storage: redis
  redis:
    host: localhost
    port: 6379
    password: ""
    db: 0
    pool_size: 10
    dial_timeout: 5s
    read_timeout: 3s
    write_timeout: 3s
```

### 限流策略

#### 令牌桶算法

```yaml
config:
  algorithm: token_bucket
  requests_per_second: 100
  burst: 200
```

#### 滑动窗口算法

```yaml
config:
  algorithm: sliding_window
  requests_per_second: 100
  window_size: 60
```

## 熔断器插件 (circuit_breaker)

### 功能描述

提供熔断保护功能，防止故障服务影响整体系统。

### 配置参数

```yaml
- name: circuit_breaker
  enabled: true
  order: 4
  config:
    failure_threshold: 5          # 失败阈值
    recovery_timeout: 60          # 恢复超时（秒）
    half_open_max_requests: 3     # 半开状态最大请求数
    error_codes: [500, 502, 503]  # 错误状态码
    timeout_errors: true          # 是否将超时视为错误
    success_threshold: 2          # 成功阈值（半开状态）
    window_size: 60               # 统计窗口大小（秒）
    min_requests: 10              # 最小请求数（开启熔断前）
```

### 熔断器状态

#### 1. 关闭状态 (Closed)
- 正常处理请求
- 统计失败次数
- 达到失败阈值时转为开启状态

#### 2. 开启状态 (Open)
- 拒绝所有请求
- 返回熔断错误
- 等待恢复超时后转为半开状态

#### 3. 半开状态 (Half-Open)
- 允许少量请求通过
- 统计成功/失败次数
- 根据结果决定状态转换

### 配置示例

```yaml
config:
  failure_threshold: 5
  recovery_timeout: 60
  half_open_max_requests: 3
  error_codes: [500, 502, 503, 504]
  timeout_errors: true
  success_threshold: 2
  window_size: 60
  min_requests: 10
```

## 跨域插件 (cors)

### 功能描述

处理跨域资源共享，支持灵活的CORS配置。

### 配置参数

```yaml
- name: cors
  enabled: true
  order: 5
  config:
    allowed_origins: ["*"]                    # 允许的源
    allowed_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]  # 允许的方法
    allowed_headers: ["*"]                    # 允许的请求头
    exposed_headers: ["Content-Length"]       # 暴露的响应头
    max_age: "12h"                           # 预检请求缓存时间
    allow_credentials: true                   # 是否允许携带凭证
    allow_private_network: false              # 是否允许私有网络
```

### 配置示例

#### 允许所有源

```yaml
config:
  allowed_origins: ["*"]
  allowed_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
  allowed_headers: ["*"]
  allow_credentials: false
```

#### 限制特定源

```yaml
config:
  allowed_origins: 
    - "https://example.com"
    - "https://api.example.com"
  allowed_methods: ["GET", "POST"]
  allowed_headers: ["Content-Type", "Authorization"]
  allow_credentials: true
```

## 日志插件 (logger)

### 功能描述

记录请求和响应日志，支持结构化日志和采样。

### 配置参数

```yaml
- name: logger
  enabled: true
  order: 1
  config:
    level: info                    # 日志级别: debug, info, warn, error
    sample_rate: 1.0               # 采样率 (0.0-1.0)
    log_headers: true              # 是否记录请求头
    log_query: true                # 是否记录查询参数
    log_body: false                # 是否记录请求体
    log_response: false            # 是否记录响应体
    buffer_size: 1000              # 缓冲区大小
    flush_interval: 5              # 刷新间隔（秒）
    skip_paths: ["/health"]        # 跳过的路径
    fields:                        # 自定义字段
      service: gateway-go
      version: 1.0.0
```

### 日志格式

#### JSON格式

```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "level": "info",
  "method": "GET",
  "path": "/api/users",
  "status_code": 200,
  "response_time": 150,
  "client_ip": "192.168.1.1",
  "user_agent": "curl/7.68.0",
  "request_id": "req-123456"
}
```

#### 文本格式

```
2024-01-01T12:00:00Z INFO GET /api/users 200 150ms 192.168.1.1 curl/7.68.0 req-123456
```

### 采样配置

```yaml
config:
  sample_rate: 0.1  # 只记录10%的请求
  sample_rules:
    - path: "/api/critical/*"
      rate: 1.0     # 关键路径100%记录
    - path: "/api/public/*"
      rate: 0.01    # 公开路径1%记录
```

## 错误处理插件 (error)

### 功能描述

统一错误处理，提供友好的错误响应。

### 配置参数

```yaml
- name: error
  enabled: true
  order: 100
  config:
    error_page_template: ""        # 错误页面模板
    error_response_format: json    # 错误响应格式: json, html
    include_stack_trace: false     # 是否包含堆栈信息
    error_codes:                   # 自定义错误码
      400: "Bad Request"
      401: "Unauthorized"
      403: "Forbidden"
      404: "Not Found"
      500: "Internal Server Error"
      502: "Bad Gateway"
      503: "Service Unavailable"
      504: "Gateway Timeout"
```

### 错误响应格式

#### JSON格式

```json
{
  "error": {
    "code": 404,
    "message": "Not Found",
    "path": "/api/unknown",
    "timestamp": "2024-01-01T12:00:00Z",
    "request_id": "req-123456"
  }
}
```

#### HTML格式

```html
<!DOCTYPE html>
<html>
<head>
    <title>404 - Not Found</title>
</head>
<body>
    <h1>404 - Not Found</h1>
    <p>The requested resource was not found.</p>
    <p>Request ID: req-123456</p>
</body>
</html>
```

## IP白名单插件 (ip_whitelist)

### 功能描述

基于IP地址控制访问权限。

### 配置参数

```yaml
- name: ip_whitelist
  enabled: false
  order: 10
  config:
    ip_whitelist:                  # IP白名单
      - "192.168.1.0/24"
      - "10.0.0.0/8"
      - "172.16.0.0/12"
    ip_blacklist:                  # IP黑名单
      - "192.168.1.100"
      - "10.0.0.50"
    mode: whitelist                # 模式: whitelist, blacklist
    real_ip_header: X-Real-IP      # 真实IP头
    forwarded_for_header: X-Forwarded-For  # 转发IP头
    trusted_proxies:               # 可信代理
      - "127.0.0.1"
      - "::1"
```

### 配置示例

#### 白名单模式

```yaml
config:
  mode: whitelist
  ip_whitelist:
    - "192.168.1.0/24"
    - "10.0.0.0/8"
  real_ip_header: X-Real-IP
```

#### 黑名单模式

```yaml
config:
  mode: blacklist
  ip_blacklist:
    - "192.168.1.100"
    - "10.0.0.50"
    - "172.16.0.100"
```

## 一致性校验插件 (consistency)

### 功能描述

提供请求签名验证，确保数据完整性。

### 配置参数

```yaml
- name: consistency
  enabled: false
  order: 20
  config:
    algorithm: hmac-sha256         # 签名算法
    secret: your-secret-key        # 密钥
    fields: [timestamp, nonce]     # 参与签名的字段
    signature_field: X-Signature   # 签名头字段
    timestamp_field: X-Timestamp   # 时间戳字段
    nonce_field: X-Nonce          # 随机数字段
    timestamp_validity: 300        # 时间戳有效期（秒）
    skip_paths: ["/health"]        # 跳过的路径
    skip_methods: ["GET", "HEAD"]  # 跳过的HTTP方法
```

### 签名生成

#### 签名算法

1. 收集参与签名的字段
2. 按字段名排序
3. 拼接字段值
4. 使用HMAC算法生成签名

#### 示例

```bash
# 请求头
X-Timestamp: 1640995200
X-Nonce: abc123
X-Signature: hmac-sha256(secret, "1640995200abc123")

# 请求
curl -H "X-Timestamp: 1640995200" \
     -H "X-Nonce: abc123" \
     -H "X-Signature: generated-signature" \
     http://localhost:8080/api/users
```

## 插件组合使用

### 常见组合

#### 1. 安全组合

```yaml
routes:
  - name: secure-api
    match:
      type: prefix
      path: /api/secure
    target:
      url: http://secure-service:8080
    plugins: ["auth", "ip_whitelist", "consistency"]
```

#### 2. 性能组合

```yaml
routes:
  - name: high-traffic-api
    match:
      type: prefix
      path: /api/public
    target:
      url: http://public-service:8080
    plugins: ["rate_limit", "circuit_breaker", "logger"]
```

#### 3. 监控组合

```yaml
routes:
  - name: monitored-api
    match:
      type: prefix
      path: /api/critical
    target:
      url: http://critical-service:8080
    plugins: ["logger", "error", "circuit_breaker"]
```

### 插件执行顺序

插件按照以下顺序执行：

1. **日志插件** (order: 1) - 记录请求开始
2. **认证插件** (order: 2) - 身份验证
3. **限流插件** (order: 3) - 请求限流
4. **熔断器插件** (order: 4) - 熔断保护
5. **跨域插件** (order: 5) - CORS处理
6. **IP白名单插件** (order: 10) - IP访问控制
7. **一致性校验插件** (order: 20) - 签名验证
8. **错误处理插件** (order: 100) - 统一错误处理

## 插件性能考虑

### 性能影响

| 插件 | 性能影响 | 建议 |
|------|----------|------|
| logger | 低 | 可启用采样 |
| auth | 中 | 使用缓存 |
| rate_limit | 中 | 使用Redis存储 |
| circuit_breaker | 低 | 内存开销小 |
| cors | 低 | 仅处理OPTIONS请求 |
| error | 低 | 仅错误时执行 |
| ip_whitelist | 低 | 使用高效匹配 |
| consistency | 中 | 计算开销 |

### 优化建议

1. **合理排序**：将高频插件放在前面
2. **启用采样**：对日志插件启用采样
3. **使用缓存**：对认证结果进行缓存
4. **外部存储**：对限流使用Redis存储
5. **跳过路径**：为不需要的路径跳过插件

## 常见问题

### Q: 插件执行顺序如何控制？
A: 通过 `order` 字段控制，数字越小优先级越高。

### Q: 如何禁用某个插件？
A: 在路由的 `plugins` 列表中不包含该插件，或在插件配置中设置 `enabled: false`。

### Q: 插件配置错误如何处理？
A: 服务启动时会验证插件配置，配置错误会导致启动失败。

### Q: 如何添加自定义插件？
A: 实现插件接口并注册到插件管理器，参考 [插件开发指南](development.md)。 