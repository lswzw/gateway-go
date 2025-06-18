# Gateway-Go API 文档

## 概述

Gateway-Go 提供了完整的 RESTful API 接口，用于配置管理、监控查询和系统管理。所有 API 都遵循标准的 HTTP 状态码和 JSON 响应格式。

## 基础信息

- **基础URL**: `http://localhost:8080`
- **内容类型**: `application/json`
- **字符编码**: `UTF-8`

## 通用响应格式

### 成功响应
```json
{
  "message": "操作成功",
  "data": {}
}
```

### 错误响应
```json
{
  "error": "错误描述",
  "code": 400
}
```

## 健康检查 API

### 健康检查

检查系统健康状态。

**请求**
```
GET /health
```

**响应**
```json
{
  "status": "ok"
}
```

## 配置管理 API

### 1. 获取配置

获取当前配置信息。

**请求**
```
GET /admin/config
```

**响应**
```json
{
  "server": {
    "port": 8080,
    "mode": "release"
  },
  "plugins": {
    "global": [...]
  },
  "routes": [...]
}
```

### 2. 更新配置

更新配置信息。

**请求**
```
POST /admin/config/update
```

**请求参数**
- `config`: 配置对象
- `comment`: 配置更新说明（可选）

**响应**
```json
{
  "message": "配置已更新"
}
```

### 3. 部分更新配置

部分更新配置信息。

**请求**
```
PATCH /admin/config/update
```

**请求参数**
- `server`: 服务器配置（可选）
- `plugins`: 插件配置（可选）
- `routes`: 路由配置（可选）
- `comment`: 配置更新说明（可选）

**响应**
```json
{
  "message": "配置已部分更新"
}
```

### 4. 回滚配置

回滚到指定的配置版本。

**请求**
```
POST /admin/config/rollback/{version}
```

**路径参数**
- `version`: 要回滚到的版本号

**响应**
```json
{
  "message": "配置已回滚"
}
```

## 使用示例

### 1. 健康检查

```bash
curl "http://localhost:8080/health"
```

### 2. 更新路由配置

```bash
curl -X POST "http://localhost:8080/admin/config/update?comment=添加用户服务路由" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "routes": [
      {
        "name": "user-service",
        "match": {
          "type": "prefix",
          "path": "/api/users"
        },
        "target": {
          "url": "http://user-service:8080",
          "timeout": 30000,
          "retries": 3
        },
        "plugins": ["auth", "rate_limit"]
      }
    ]
  }'
```

## 注意事项

1. **配置热更新**：大部分配置支持热更新，但某些配置（如端口）需要重启服务
2. **版本管理**：建议在更新配置时添加有意义的注释
3. **权限控制**：请确保只有授权用户能够访问配置管理 API
4. **监控告警**：建议配置监控告警，及时发现系统异常
5. **备份恢复**：定期备份配置文件，以便在需要时快速恢复

## 错误码说明

| 状态码 | 说明 | 描述 |
|--------|------|------|
| 200 | OK | 请求成功 |
| 400 | Bad Request | 请求参数错误 |
| 401 | Unauthorized | 未授权访问 |
| 403 | Forbidden | 禁止访问 |
| 404 | Not Found | 资源不存在 |
| 408 | Request Timeout | 请求超时 |
| 429 | Too Many Requests | 请求过于频繁 |
| 500 | Internal Server Error | 服务器内部错误 |
| 502 | Bad Gateway | 网关错误 |
| 503 | Service Unavailable | 服务暂时不可用 |

## 认证和授权

### Token 认证

对于需要认证的 API，请在请求头中包含有效的认证令牌：

```
Authorization: Bearer <your-token>
```

### 权限控制

- 配置管理 API 需要管理员权限
- 监控 API 需要监控权限
- 健康检查 API 无需认证

## 限流说明

API 接口受到限流控制：

- 默认限制：每秒 10 个请求
- 突发流量：最多 20 个请求
- 限流策略：基于客户端 IP 地址

当触发限流时，会返回 429 状态码：

```json
{
  "error": "请求过于频繁，请稍后重试"
}
```

## 使用示例

### 1. 更新限流配置

```bash
curl -X PATCH "http://localhost:8080/admin/config/update?comment=调整限流配置" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "middleware": {
      "rate_limit": {
        "requests_per_second": 50,
        "burst": 100
      }
    }
  }'
```

### 2. 添加新路由

```bash
curl -X POST "http://localhost:8080/admin/config/update?comment=添加用户服务路由" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "routes": [
      {
        "name": "user-service",
        "match": {
          "type": "prefix",
          "path": "/api/users"
        },
        "target": {
          "url": "http://user-service:8080",
          "timeout": 30000,
          "retries": 3
        },
        "plugins": ["auth", "rate_limit"]
      }
    ]
  }'
```

### 3. 查看监控指标

```bash
curl "http://localhost:8080/metrics/prometheus"
```

## 注意事项

1. **配置热更新**：大部分配置支持热更新，但某些配置（如端口）需要重启服务
2. **版本管理**：建议在更新配置时添加有意义的注释
3. **权限控制**：请确保只有授权用户能够访问配置管理 API
4. **监控告警**：建议配置监控告警，及时发现系统异常
5. **备份恢复**：定期备份配置文件，以便在需要时快速恢复 