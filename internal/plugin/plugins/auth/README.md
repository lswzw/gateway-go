# 认证插件（auth）

## 一、概述
认证插件用于对进入网关的请求进行身份认证，支持Token（JWT）和Basic两种认证方式。可灵活配置认证类型、密钥、角色权限等，保障API安全。

## 二、设计目标
1. 支持多种认证方式（Token、Basic）
2. 灵活配置认证参数
3. 支持角色权限控制
4. 支持跳过部分路径
5. 完善的错误处理和日志记录

## 三、流程图
1. 客户端发起请求
2. 插件拦截请求
3. 判断认证类型
   - Token：校验JWT
   - Basic：校验用户名密码
4. 校验通过则放行，否则返回错误
5. 后端服务处理请求

## 四、配置参数

| 名称              | 数据类型         | 必填 | 默认值         | 描述                         |
|-------------------|----------------|------|----------------|------------------------------|
| type              | string         | 否   | token          | 认证类型：token/basic         |
| token_header      | string         | 否   | Authorization  | Token请求头名称              |
| token_prefix      | string         | 否   | Bearer         | Token前缀                    |
| secret_key        | string         | 否   | -              | JWT密钥                      |
| token_expiry      | int            | 否   | 3600           | Token过期时间（秒）           |
| issuer            | string         | 否   | gateway-go     | Token发行者                  |
| audience          | string         | 否   | api-users      | Token受众                    |
| algorithms        | array of string| 否   | ["HS256"]      | 支持的算法                   |
| user_claim        | string         | 否   | sub            | 用户标识字段                 |
| roles_claim       | string         | 否   | roles          | 角色字段                     |
| required_roles    | array of string| 否   | []             | 必需角色列表                 |
| skip_paths        | array of string| 否   | []             | 跳过认证的路径               |
| users             | array of object| 否   | -              | Basic认证用户列表             |

## 五、配置示例

#### Token认证
```yaml
- name: auth
  enabled: true
  order: 2
  config:
    type: token
    token_header: Authorization
    token_prefix: Bearer
    secret_key: your-secret-key
    token_expiry: 3600
    required_roles: ["admin"]
    skip_paths: ["/health"]
```

#### Basic认证
```yaml
- name: auth
  enabled: true
  order: 2
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

## 六、运行属性
- 插件执行阶段：认证阶段
- 插件执行优先级：2

## 七、请求示例
```bash
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/users
curl -u admin:admin123 http://localhost:8080/api/users
```

## 八、处理流程
1. 校验配置参数
2. 判断请求路径是否跳过
3. 按type选择认证方式
4. 校验Token或用户名密码
5. 校验角色权限
6. 认证通过则放行，否则返回错误

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 400         | Invalid Signature  | JWT解析失败            |
| 401         | Unauthorized       | 未提供认证信息/认证失败|
| 403         | Forbidden          | 权限不足               |

## 十、插件配置
在路由或全局plugins中添加`auth`插件即可。 