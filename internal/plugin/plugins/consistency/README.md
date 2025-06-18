# 一致性校验插件 (Consistency Check Plugin)

一致性校验插件用于验证请求和响应中关键字段的一致性，支持多种校验算法和灵活的配置选项。

## 功能特点

- 🔒 支持请求和响应的一致性校验
- 🔑 多种校验算法（HMAC-SHA256、MD5、RSA、ECDSA、Ed25519）
- ⚙️ 可配置的校验字段
- 🚫 校验失败时直接返回错误
- 🔄 支持自定义签名字段名
- 🛡️ 防重放攻击机制
- ⏰ 请求时间戳验证

## 配置说明

```yaml
plugins:
  - name: consistency
    enabled: true
    order: 1
    config:
      # 校验算法：hmac-sha256、md5、rsa、ecdsa、ed25519
      algorithm: hmac-sha256
      
      # 密钥（用于 HMAC-SHA256 和 MD5）
      secret: your-secret-key
      
      # 公钥（用于 RSA、ECDSA、Ed25519）
      public_key: |
        -----BEGIN PUBLIC KEY-----
        YOUR_PUBLIC_KEY_HERE
        -----END PUBLIC KEY-----
      
      # 需要校验的字段
      fields:
        - timestamp
        - nonce
      
      # 签名字段名
      signature_field: X-Signature
      
      # 是否校验响应
      check_response: false
      
      # 时间戳有效期（秒）
      timestamp_validity: 300
```

## 校验算法

### HMAC-SHA256
- 使用 HMAC-SHA256 算法
- 需要密钥
- 安全性高

### MD5
- 使用 MD5 算法
- 计算速度快
- 适合简单场景

### RSA
- 使用 RSA 算法
- 需要公钥
- 支持数字签名验证

### ECDSA
- 使用椭圆曲线数字签名算法
- 需要公钥
- 安全性高，密钥长度短

### Ed25519
- 使用 Ed25519 算法
- 需要公钥
- 高性能，安全性高

## 安全特性

### 防重放攻击
- 使用 nonce 机制
- 记录已使用的 nonce
- 自动清理过期的 nonce

### 时间戳验证
- 验证请求时间戳
- 可配置时间戳有效期
- 防止重放攻击

## 使用示例

1. 注册插件：
```go
consistencyPlugin := consistency.New()
pluginManager.Register(consistencyPlugin)
```

2. 配置插件：
```yaml
plugins:
  - name: consistency
    enabled: true
    order: 1
    config:
      algorithm: hmac-sha256
      secret: your-secret-key
      fields:
        - timestamp
        - nonce
      signature_field: X-Signature
      timestamp_validity: 300
```

3. 发送请求：
```bash
curl -X POST http://localhost:8080/api \
  -H "X-Timestamp: 1234567890" \
  -H "X-Nonce: abc123" \
  -H "X-Signature: calculated-signature" \
  -d '{"data": "test"}'
```

## 签名计算

### 请求签名
1. 获取需要校验的字段值
2. 按顺序拼接字段值（使用 & 连接）
3. 使用配置的算法和密钥/公钥计算签名

### 响应签名
1. 获取响应体内容
2. 使用配置的算法和密钥/公钥计算签名
3. 将签名添加到响应头

## 错误处理

### 请求校验失败
- 返回 400 Bad Request
- 错误信息说明具体原因

### 响应校验失败
- 返回 500 Internal Server Error
- 记录错误日志

## 性能优化

1. 缓存处理
   - 缓存常用签名
   - 避免重复计算

2. 并发处理
   - 线程安全的实现
   - 最小化锁竞争

## 注意事项

1. 安全建议
   - 使用安全的密钥
   - 定期更新密钥
   - 使用 HTTPS
   - 定期清理过期的 nonce

2. 配置建议
   - 选择合适的算法
   - 设置合理的字段
   - 考虑性能影响
   - 设置合适的时间戳有效期

3. 运维建议
   - 监控校验失败率
   - 记录错误日志
   - 及时处理异常 