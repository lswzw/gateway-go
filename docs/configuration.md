# 配置管理

## 概述

Gateway-Go 采用 YAML 格式的配置文件，支持热重载和动态配置更新。配置系统提供了灵活的配置管理能力，包括服务器配置、插件配置、路由配置等。

## 配置文件结构

### 完整配置示例

```yaml
# 服务器配置
server:
  port: 8080
  mode: debug  # debug, release
  read_timeout: "60s"
  write_timeout: "60s"
  max_header_bytes: 1048576

# 日志配置
log:
  level: info  # debug, info, warn, error
  format: json  # json, text
  output: stdout  # stdout, stderr, file
  file_path: /var/log/gateway-go/gateway.log
  max_size: 100  # MB
  max_age: 30    # days
  max_backups: 10

# 插件配置
plugins:
  # 可用插件定义（仅定义插件是否可用，不自动生效）
  available:
    # 日志插件
    - name: logger
      enabled: true
      order: 1
      config:
        level: info
        sample_rate: 1.0
        log_headers: true
        log_query: true
        log_body: false
        buffer_size: 1000
        flush_interval: 5
        skip_paths: ["/health"]

    # 认证插件
    - name: auth
      enabled: true
      order: 2
      config:
        type: token  # token, basic
        token_header: Authorization
        token_prefix: Bearer
        secret_key: your-secret-key
        token_expiry: 3600

    # 限流插件
    - name: rate_limit
      enabled: true
      order: 3
      config:
        requests_per_second: 100
        burst: 200
        dimension: ip  # ip, user, global
        storage: memory  # memory, redis

    # 熔断器插件
    - name: circuit_breaker
      enabled: true
      order: 4
      config:
        failure_threshold: 5
        recovery_timeout: 60
        half_open_max_requests: 3

    # 跨域插件
    - name: cors
      enabled: true
      order: 5
      config:
        allowed_origins: ["*"]
        allowed_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
        allowed_headers: ["*"]
        exposed_headers: ["Content-Length"]
        max_age: "12h"
        allow_credentials: true

    # 错误处理插件
    - name: error
      enabled: true
      order: 100
      config:
        error_page_template: ""
        error_response_format: json

    # IP白名单插件
    - name: ip_whitelist
      enabled: false
      order: 10
      config:
        ip_whitelist: []
        ip_blacklist: []

    # 一致性校验插件
    - name: consistency
      enabled: false
      order: 20
      config:
        algorithm: hmac-sha256
        secret: your-secret-key
        fields: [timestamp, nonce]
        signature_field: X-Signature
        timestamp_validity: 300

# 路由配置
routes:
  # API服务路由
  - name: api-service
    match:
      type: prefix  # exact, prefix, regex, wildcard
      path: /api
      host: ""  # 可选，支持通配符
      priority: 90
    target:
      url: http://192.168.100.69:80
      timeout: 30000  # 毫秒
      retries: 3
      retry_delay: 1000  # 毫秒
      health_check:
        enabled: true
        path: /health
        interval: 30
        timeout: 5
    plugins: ["auth", "rate_limit", "circuit_breaker"]  # 该路由使用的插件列表

  # 健康检查路由
  - name: health-check
    match:
      type: exact
      path: /health
      priority: 100
    target:
      url: http://127.0.0.1:8080
      timeout: 30000
      retries: 3
    plugins: []  # 空表示不使用插件

  # 多租户支持
  - name: tenant1-service
    match:
      type: prefix
      path: /api
      host: "*.tenant1.example.com"
      priority: 90
    target:
      url: http://tenant1-service:8080
    plugins: ["auth", "rate_limit"]  # 只使用认证和限流插件
```

## 配置详解

### 服务器配置 (server)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| port | int | 8080 | 服务器监听端口 |
| mode | string | debug | 运行模式 (debug/release) |
| read_timeout | string | 60s | 读取超时时间 |
| write_timeout | string | 60s | 写入超时时间 |
| max_header_bytes | int | 1048576 | 最大请求头大小 |

### 日志配置 (log)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| level | string | info | 日志级别 |
| format | string | json | 日志格式 (json/text) |
| output | string | stdout | 输出目标 |
| file_path | string | - | 文件路径（当output为file时） |
| max_size | int | 100 | 单个日志文件最大大小(MB) |
| max_age | int | 30 | 日志文件保留天数 |
| max_backups | int | 10 | 保留的备份文件数量 |

### 插件配置 (plugins.available)

插件配置采用声明式方式，每个插件包含以下字段：

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| name | string | 是 | 插件名称 |
| enabled | bool | 是 | 是否启用 |
| order | int | 是 | 执行顺序（数字越小优先级越高） |
| config | object | 否 | 插件特定配置 |

**重要说明**：
- `enabled: true` 表示插件可用，但不会自动对所有路由生效
- 插件要生效必须在路由的 `plugins` 字段中明确指定
- 这种设计实现了精准的插件控制，避免全局插件对所有路由的强制影响

### 路由配置 (routes)

每个路由包含以下配置：

#### 匹配规则 (match)

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| type | string | 是 | 匹配类型 (exact/prefix/regex/wildcard) |
| path | string | 是 | 匹配路径 |
| host | string | 否 | 匹配主机名（支持通配符） |
| priority | int | 否 | 优先级（数字越大优先级越高） |

**匹配类型说明**：
- `exact`: 精确匹配路径
- `prefix`: 前缀匹配
- `regex`: 正则表达式匹配
- `wildcard`: 通配符匹配

#### 目标配置 (target)

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| url | string | - | 目标服务URL |
| timeout | int | 30000 | 请求超时时间（毫秒） |
| retries | int | 3 | 重试次数 |
| retry_delay | int | 1000 | 重试延迟（毫秒） |
| health_check | object | - | 健康检查配置 |

#### 插件配置 (plugins)

路由级别的插件配置，指定该路由使用的插件列表。插件按照数组中的顺序执行。

## 配置验证

### 启动时验证

服务启动时会进行以下验证：

1. **配置文件格式验证**：确保 YAML 格式正确
2. **必需字段验证**：检查必需字段是否存在
3. **插件配置验证**：
   - 路由引用的插件必须在 `available` 中定义
   - 路由引用的插件必须 `enabled: true`
4. **路由配置验证**：
   - 路由名称唯一性
   - 目标URL格式正确
   - 匹配规则有效性

### 运行时验证

配置热重载时会进行相同的验证，如果验证失败：

1. 记录错误日志
2. 保持现有配置不变
3. 继续使用旧配置运行

## 动态配置

### 热重载机制

服务支持配置文件的动态加载：

1. **文件监控**：使用 `fsnotify` 监控配置文件变化
2. **配置解析**：解析新的配置文件
3. **配置验证**：验证新配置的有效性
4. **配置更新**：更新运行时配置
5. **路由重载**：重新注册所有路由
6. **插件重载**：重新加载插件配置

### 配置更新流程

```mermaid
graph TD
    A[配置文件变更] --> B[检测文件变化]
    B --> C[解析新配置]
    C --> D[验证配置]
    D --> E{验证通过?}
    E -->|是| F[更新运行时配置]
    E -->|否| G[记录错误日志]
    F --> H[重新加载路由]
    H --> I[重新加载插件]
    I --> J[通知配置更新]
    G --> K[保持旧配置]
```

### 配置管理API

提供以下管理API：

```bash
# 获取当前配置
GET /admin/config/current

# 重新加载配置
POST /admin/config/reload

# 测试配置
POST /admin/config/test

# 获取配置版本历史
GET /admin/config/history

# 回滚到指定版本
POST /admin/config/rollback/{version}
```

## 配置最佳实践

### 1. 配置文件组织

```yaml
# 建议的配置文件结构
server:
  # 服务器基础配置

log:
  # 日志配置

plugins:
  available:
    # 按功能分组插件
    # 安全相关插件
    - name: auth
    - name: ip_whitelist
    
    # 流量控制插件
    - name: rate_limit
    - name: circuit_breaker
    
    # 监控相关插件
    - name: logger
    - name: error

routes:
  # 按服务类型分组路由
  # 内部服务
  - name: internal-api
  - name: health-check
  
  # 外部API
  - name: public-api
  - name: partner-api
```

### 2. 插件配置原则

- **最小权限原则**：只启用路由实际需要的插件
- **性能优先**：将高频插件放在前面执行
- **配置分离**：插件配置和路由配置分离管理

### 3. 路由配置原则

- **优先级合理**：为不同路由设置合理的优先级
- **命名规范**：使用有意义的路由名称
- **健康检查**：为重要服务配置健康检查

### 4. 安全配置

- **敏感信息**：不要在配置文件中硬编码敏感信息
- **访问控制**：限制配置管理API的访问权限
- **配置备份**：定期备份配置文件

## 常见配置问题

### Q: 配置文件修改后不生效？
A: 检查以下几点：
- 配置文件格式是否正确
- 文件权限是否正确
- 服务是否有文件读取权限
- 查看服务日志中的错误信息

### Q: 插件配置错误？
A: 常见错误：
- 路由引用了未定义的插件
- 插件配置格式不正确
- 插件依赖关系错误

### Q: 路由匹配失败？
A: 检查：
- 路由匹配规则是否正确
- 路由优先级是否合理
- 目标服务是否可访问

### Q: 配置热重载失败？
A: 可能原因：
- 新配置验证失败
- 文件权限问题
- 磁盘空间不足 