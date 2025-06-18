# 一致性校验插件（consistency）

## 一、概述
一致性校验插件用于对请求进行签名验证，确保数据完整性和防止重放攻击，支持多种签名算法和字段配置。

## 二、设计目标
1. 支持多种签名算法（如HMAC-SHA256）
2. 支持自定义签名字段
3. 支持时间戳和随机数校验
4. 支持跳过部分路径和方法
5. 完善的错误处理和日志记录

## 三、流程图
1. 客户端发起请求
2. 插件拦截请求
3. 收集签名字段
4. 校验签名、时间戳、随机数
5. 校验通过则放行，否则拒绝

## 四、配置参数

| 名称                | 数据类型         | 必填 | 默认值         | 描述                         |
|---------------------|----------------|------|----------------|------------------------------|
| algorithm           | string         | 否   | hmac-sha256    | 签名算法                     |
| secret              | string         | 是   | -              | 密钥                         |
| fields              | array of string| 是   | -              | 参与签名的字段               |
| signature_field     | string         | 否   | X-Signature    | 签名头字段                   |
| timestamp_field     | string         | 否   | X-Timestamp    | 时间戳字段                   |
| nonce_field         | string         | 否   | X-Nonce        | 随机数字段                   |
| timestamp_validity  | int            | 否   | 300            | 时间戳有效期（秒）            |
| skip_paths          | array of string| 否   | []             | 跳过的路径                   |
| skip_methods        | array of string| 否   | []             | 跳过的HTTP方法               |

## 五、配置示例

```yaml
- name: consistency
  enabled: true
  order: 20
  config:
    algorithm: hmac-sha256
    secret: your-secret-key
    fields: [timestamp, nonce]
    signature_field: X-Signature
    timestamp_field: X-Timestamp
    nonce_field: X-Nonce
    timestamp_validity: 300
    skip_paths: ["/health"]
    skip_methods: ["GET", "HEAD"]
```

## 六、运行属性
- 插件执行阶段：安全控制阶段
- 插件执行优先级：20

## 七、请求示例
```bash
curl -H "X-Timestamp: 1640995200" \
     -H "X-Nonce: abc123" \
     -H "X-Signature: <签名值>" \
     http://localhost:8080/api/users
```

## 八、处理流程
1. 校验配置参数
2. 收集签名字段
3. 校验签名、时间戳、随机数
4. 校验通过则放行，否则拒绝

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 400         | Bad Signature      | 签名校验失败           |
| 401         | Expired Timestamp  | 时间戳无效             |
| 403         | Forbidden          | 签名字段缺失/不合法     |

## 十、插件配置
在路由或全局plugins中添加`consistency`插件即可。 