# Gateway-Go 最佳实践

## 概述

本文档提供了 Gateway-Go 的最佳实践指南，包括配置优化、性能调优、安全加固、监控告警等方面的建议，帮助您构建高性能、高可用的 API 网关。

## 配置最佳实践

### 1. 配置文件管理

#### 环境分离
```yaml
# config/config.dev.yaml
server:
  port: 8080
  mode: debug

log:
  level: debug
  format: console

# config/config.prod.yaml
server:
  port: 80
  mode: release

log:
  level: warn
  format: json
  output: /var/log/gateway-go/gateway.log
```

#### 配置验证
```bash
# 验证配置文件语法
gateway-go --validate-config config/config.yaml

# 验证配置内容
gateway-go --check-config config/config.yaml
```

#### 配置版本控制
```bash
# 使用 Git 管理配置
git add config/
git commit -m "feat: 更新生产环境配置"

# 使用配置标签
git tag -a v1.0.0-config -m "生产环境配置 v1.0.0"
```

### 2. 路由配置优化

#### 路由优先级
```yaml
routes:
  # 高优先级：精确匹配
  - name: health-check
    match:
      type: exact
      path: /health
      priority: 100
    target:
      url: http://127.0.0.1:8080
    plugins: []

  # 中优先级：API 路由
  - name: api-v1
    match:
      type: prefix
      path: /api/v1
      priority: 90
    target:
      url: http://api-v1:8080
    plugins: ["auth", "rate_limit"]

  # 低优先级：默认路由
  - name: default
    match:
      type: prefix
      path: /
      priority: 0
    target:
      url: http://default:8080
    plugins: []
```

#### 路由分组
```yaml
routes:
  # 内部服务
  - name: internal-services
    match:
      type: prefix
      path: /internal
    target:
      url: http://internal:8080
    plugins: ["ipwhitelist"]

  # 外部 API
  - name: external-api
    match:
      type: prefix
      path: /api
    target:
      url: http://external:8080
    plugins: ["auth", "rate_limit", "cors"]

  # 静态资源
  - name: static-assets
    match:
      type: prefix
      path: /static
    target:
      url: http://static:8080
    plugins: []
```

### 3. 中间件配置

#### 中间件顺序
```yaml
middleware:
  # 1. 错误处理（最外层）
  error:
    enabled: true

  # 2. 日志记录
  logger:
    enabled: true
    format: "[GIN] | %d | %s | %s | %s | %s"
    skip_paths: ["/health"]
    level: "info"

  # 3. 限流控制
  rate_limit:
    enabled: true
    requests_per_second: 100
    burst: 200
    ip_based: true

  # 4. 熔断保护
  circuit_breaker:
    enabled: true
    failure_threshold: 5
    recovery_timeout: "30s"
    half_open_quota: 2
```

#### 限流策略
```yaml
middleware:
  rate_limit:
    enabled: true
    # 全局限流
    global:
      requests_per_second: 1000
      burst: 2000
    
    # 按路径限流
    paths:
      /api/users:
        requests_per_second: 100
        burst: 200
      /api/orders:
        requests_per_second: 50
        burst: 100
    
    # 按用户限流
    users:
      requests_per_second: 10
      burst: 20
```

## 性能优化最佳实践

### 1. 系统级优化

#### 内核参数调优
```bash
# /etc/sysctl.conf
# 网络连接优化
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 5000
net.ipv4.tcp_max_syn_backlog = 65535
net.ipv4.tcp_fin_timeout = 30
net.ipv4.tcp_keepalive_time = 1200
net.ipv4.tcp_max_tw_buckets = 5000

# 内存优化
vm.swappiness = 10
vm.dirty_ratio = 15
vm.dirty_background_ratio = 5

# 文件系统优化
fs.file-max = 1000000
```

#### 文件描述符限制
```bash
# /etc/security/limits.conf
gateway soft nofile 65535
gateway hard nofile 65535
gateway soft nproc 65535
gateway hard nproc 65535
```

### 2. 应用级优化

#### 连接池配置
```yaml
server:
  # 连接池配置
  connection_pool:
    max_idle_conns: 100
    max_open_conns: 1000
    conn_max_lifetime: "1h"
    idle_timeout: "90s"

  # 超时配置
  timeouts:
    read_timeout: "30s"
    write_timeout: "30s"
    idle_timeout: "60s"
```

## 安全最佳实践

### 1. 认证和授权

#### JWT 配置
```yaml
plugins:
  global:
    - name: auth
      enabled: true
      config:
        type: jwt
        jwt:
          secret: "${JWT_SECRET}"  # 使用环境变量
          expire: "24h"
          issuer: "gateway"
          audience: "api"
        # 权限控制
        permissions:
          admin:
            paths: ["/admin/*"]
            methods: ["GET", "POST", "PUT", "DELETE"]
          user:
            paths: ["/api/*"]
            methods: ["GET", "POST"]
          readonly:
            paths: ["/api/public/*"]
            methods: ["GET"]
```

#### API Key 认证
```yaml
plugins:
  global:
    - name: auth
      enabled: true
      config:
        type: apikey
        apikey:
          header: "X-API-Key"
          # API Key 存储
          storage:
            type: memory
            # 内存存储配置
            memory:
              cleanup_interval: "1h"
        # 速率限制
        rate_limit:
          requests_per_second: 100
          burst: 200
```

### 2. 网络安全

#### IP 白名单
```yaml
plugins:
  global:
    - name: ipwhitelist
      enabled: true
      config:
        # 允许的 IP 范围
        allowed_ips:
          - "192.168.1.0/24"
          - "10.0.0.0/8"
          - "172.16.0.0/12"
        # 特殊路径
        paths:
          /admin/*:
            - "192.168.1.100"
            - "10.0.0.50"
```

#### CORS 配置
```yaml
plugins:
  global:
    - name: cors
      enabled: true
      config:
        allowed_origins:
          - "https://example.com"
          - "https://api.example.com"
        allowed_methods:
          - "GET"
          - "POST"
          - "PUT"
          - "DELETE"
          - "OPTIONS"
        allowed_headers:
          - "Authorization"
          - "Content-Type"
          - "X-API-Key"
        exposed_headers:
          - "X-Total-Count"
          - "X-Page-Count"
        max_age: "12h"
        credentials: true
```

### 3. 数据保护

#### 敏感信息脱敏
```yaml
middleware:
  logger:
    enabled: true
    # 敏感字段脱敏
    sensitive_fields:
      - "password"
      - "token"
      - "secret"
      - "authorization"
    # 脱敏规则
    mask_rules:
      password: "***"
      token: "***"
      secret: "***"
```

#### 请求验证
```yaml
plugins:
  global:
    - name: validation
      enabled: true
      config:
        # 请求大小限制
        max_request_size: "10MB"
        # 文件上传限制
        max_file_size: "5MB"
        # 允许的文件类型
        allowed_file_types:
          - "image/jpeg"
          - "image/png"
          - "application/pdf"
```

## 高可用最佳实践

### 1. 负载均衡

#### 健康检查配置
```yaml
server:
  health_check:
    enabled: true
    path: "/health"
    interval: "30s"
    timeout: "5s"
    unhealthy_threshold: 3
    healthy_threshold: 2
    # 自定义健康检查
    custom_checks:
      - name: "database"
        path: "/health/db"
        timeout: "3s"
```

#### 故障转移策略
```yaml
routes:
  - name: api-service
    match:
      type: prefix
      path: /api
    target:
      # 主服务
      primary:
        url: "http://api-primary:8080"
        weight: 100
      # 备用服务
      backup:
        url: "http://api-backup:8080"
        weight: 0
      # 故障转移配置
      failover:
        enabled: true
        health_check_interval: "10s"
        failure_threshold: 3
        recovery_threshold: 2
```

### 2. 熔断器配置

#### 熔断策略
```yaml
middleware:
  circuit_breaker:
    enabled: true
    # 全局熔断器
    global:
      failure_threshold: 10
      recovery_timeout: "60s"
      half_open_quota: 5
      success_threshold: 3
    
    # 按服务熔断
    services:
      api-users:
        failure_threshold: 5
        recovery_timeout: "30s"
        half_open_quota: 2
        success_threshold: 2
      
      api-orders:
        failure_threshold: 3
        recovery_timeout: "60s"
        half_open_quota: 1
        success_threshold: 3
```

### 3. 降级策略

#### 降级配置
```yaml
plugins:
  global:
    - name: fallback
      enabled: true
      config:
        # 降级策略
        strategies:
          # 默认响应降级
          default:
            enabled: true
            responses:
              /api/users:
                status: 200
                body: '{"message": "服务暂时不可用，请稍后重试"}'
              /api/orders:
                status: 503
                body: '{"error": "订单服务暂时不可用"}'
```