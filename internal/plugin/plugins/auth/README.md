# 认证插件 (Auth Plugin)

认证插件提供了灵活的请求认证机制，支持多种认证方式。

## 功能特点

- 🔐 支持多种认证方式
  - Token 认证
  - Basic 认证
- ⚙️ 可配置的认证参数
- 🔄 支持自定义认证逻辑扩展

## 配置说明

```yaml
plugins:
  - name: auth
    enabled: true
    order: 1
    config:
      # 认证类型：token 或 basic
      type: token
      
      # Token 认证配置
      token_header: Authorization
      token_prefix: Bearer
      
      # Basic 认证配置
      realm: API Service
```

## 认证方式

### Token 认证

1. 请求头格式：
```
Authorization: Bearer <token>
```

2. 配置参数：
- `type`: 设置为 "token"
- `token_header`: 指定请求头名称
- `token_prefix`: 指定 token 前缀

### Basic 认证

1. 请求头格式：
```
Authorization: Basic <base64(username:password)>
```

2. 配置参数：
- `type`: 设置为 "basic"
- `realm`: 指定认证域

## 使用示例

1. 注册插件：
```go
authPlugin := auth.New()
pluginManager.Register(authPlugin)
```

2. 配置插件：
```yaml
plugins:
  - name: auth
    enabled: true
    order: 1
    config:
      type: token
      token_header: Authorization
      token_prefix: Bearer
```

3. 发送认证请求：
```bash
# Token 认证
curl -H "Authorization: Bearer your-token" http://api.example.com

# Basic 认证
curl -u username:password http://api.example.com
```

## 扩展开发

1. 实现新的认证方式：
```go
type CustomAuth struct {
    config map[string]interface{}
}

func (a *CustomAuth) Handle(ctx *gin.Context) error {
    // 实现自定义认证逻辑
    return nil
}
```

2. 注册自定义认证：
```go
customAuth := &CustomAuth{}
pluginManager.Register(customAuth)
```

## 注意事项

1. 安全性
   - 使用 HTTPS 传输
   - 定期轮换 token
   - 设置合理的 token 过期时间

2. 性能
   - 使用缓存存储 token 验证结果
   - 避免频繁的数据库查询

3. 错误处理
   - 返回适当的 HTTP 状态码
   - 提供清晰的错误信息 