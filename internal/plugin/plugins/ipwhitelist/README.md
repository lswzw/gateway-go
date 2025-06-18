# IP 白名单插件 (IP Whitelist Plugin)

IP 白名单插件用于限制 API 访问，只允许指定的 IP 地址访问。

## 功能特点

- 🌐 支持 IP 地址白名单
- 📝 支持 CIDR 格式
- 🔄 动态更新白名单
- ⚡ 高性能实现
- 🛡️ 线程安全

## 配置说明

```yaml
plugins:
  - name: ip_whitelist
    enabled: true
    order: 1
    config:
      # IP 白名单列表
      ip_whitelist:
        - 192.168.1.1
        - 10.0.0.0/24
        - 172.16.0.0/16
```

## 白名单格式

### 单个 IP
- 支持 IPv4 和 IPv6
- 示例：`192.168.1.1`

### CIDR 格式
- 支持网段表示
- 示例：`10.0.0.0/24`

## 使用示例

1. 注册插件：
```go
ipWhitelistPlugin := ipwhitelist.New()
pluginManager.Register(ipWhitelistPlugin)
```

2. 配置插件：
```yaml
plugins:
  - name: ip_whitelist
    enabled: true
    order: 1
    config:
      ip_whitelist:
        - 192.168.1.1
        - 10.0.0.0/24
```

3. 动态更新白名单：
```go
// 添加 IP
plugin.AddIP("192.168.1.2")

// 移除 IP
plugin.RemoveIP("192.168.1.1")

// 获取白名单
whitelist := plugin.GetWhitelist()
```

## 访问控制

### 白名单为空
- 允许所有 IP 访问
- 适用于开发环境

### 白名单非空
- 只允许白名单中的 IP 访问
- 其他 IP 返回 403 错误

## 错误处理

### IP 不在白名单中
- 返回 403 Forbidden
- 错误信息说明原因

## 性能优化

1. 内存管理
   - 使用 sync.Map 存储白名单
   - 避免频繁的内存分配
   - 高效的 IP 匹配算法

2. 并发处理
   - 线程安全的实现
   - 支持并发更新
   - 最小化锁竞争

## 注意事项

1. 安全建议
   - 定期更新白名单
   - 使用最小权限原则
   - 记录访问日志

2. 配置建议
   - 使用 CIDR 格式减少配置量
   - 定期清理无效 IP
   - 考虑使用动态更新

3. 运维建议
   - 监控白名单更新
   - 记录拒绝访问日志
   - 定期检查白名单有效性 