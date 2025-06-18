# interface_auth 外部接口认证鉴权插件

## 一、概述
本插件用于对网关请求进行外部接口认证。默认所有接口都需要认证，支持配置白名单接口跳过认证。可灵活配置认证接口、认证服务地址，实现接口级别的安全管控。

## 二、设计目标
- 支持接口白名单灵活配置
- 支持外部认证服务对接
- 高性能、并发安全
- 便于扩展和维护
- 认证失败返回标准错误码

## 三、处理流程
```mermaid
graph TD
    A[请求进入网关] --> B{是否在白名单}
    B -- 是 --> C[直接放行]
    B -- 否 --> F[调用外部认证服务]
    F -- 认证成功 --> C
    F -- 认证失败 --> E[直接拒绝]
```

## 四、配置参数

| 名称                | 数据类型           | 必填 | 默认值 | 描述                       |
|---------------------|------------------|------|--------|----------------------------|
| white_interfaces    | array of string  | 是   | -      | 白名单接口，支持通配符*     |
| consumers           | object           | 是   | -      | 鉴权服务配置               |
| consumers.host      | string           | 是   | -      | 认证服务主机地址           |
| consumers.auth_api  | string           | 是   | -      | 认证服务API路径            |

## 五、配置示例
```yaml
plugins:
  available:
    - name: interface_auth
      enabled: true
      order: 900
      config:
        consumers:
          auth_api: "/auth/realms/paas/protocol/openid-connect/unauthorized"
          host: "center.higress-test.svc.cluster.local:8080"
        white_interfaces:
          - "/info"
          - "/health"
          - "/verification/*"
```

## 六、运行属性
- 插件执行阶段：认证阶段
- 插件执行优先级：900

## 七、请求示例
```bash
curl -H "Authorization: Bearer <token>" http://your-gateway-domain/info
```

## 八、处理流程
1. 启动插件，加载并校验配置
2. 拦截每个请求，解析访问接口
3. 检查是否在白名单，若是则放行
4. 其他情况调用外部认证服务
5. 认证服务返回false放行，返回true拒绝

## 九、错误码
| HTTP 状态码 | 出错信息                        | 说明                       |
|-------------|-------------------------------|----------------------------|
| 401         | Token missing or invalid      | token缺失或无效            |
| 403         | Forbidden: Access denied      | 黑名单或认证失败           |
| 500         | Auth service call failed      | 调用认证服务失败           |
| 500         | Internal error: no response body | 认证服务返回空响应体   |

## 十、插件配置
- 在全局plugins.available或路由plugins中添加`interface_auth`插件即可。
- 推荐全局开启，按需在路由粒度覆盖。

# keyclock 外部接口认证鉴权 #

| title | keywords | description |
| :-----:| :----: | :----: |
| 外部接口认证鉴权 | auth | 白名单不需要认证，其他接口需要认证 |



# 功能说明 #

对请求的接口做认证操作，支持白名单、黑名单和外部认证服务。




![alt text](image-1.png)
# 处理数据范围 #

* 协议：request请求
* jsonpath：处理指定接口
* raw：整个请求


# 接口认证拦截 #

* 网关请求拦截，失败返回预设错误信息。成功继续请求访问

# 运行属性 #

插件执行阶段：认证阶段   插件执行优先级：900

------------------------------------------------------------
# 插件运行阶段配置说明 #

## 认证阶段 ##
* 进行流量的初步处理，比如限流、鉴权等。
* 对请求头、路径等信息进行修改或验证。


#  配置字段  #
			
| 名称          | 数据类型        | 填写要求 | 默认值 | 描述                                                                                                                |
| ------------- | --------------- | -------- | ------ | ------------------------------------------------------------------------------------------------------------------- |
| `white_interfaces` | array of string            | 必填                  | -      | 白名单接口，支持通配符* |
| `consumers`  | object  | 必填 | - | 鉴权服务配置 |

 --------------------------------------------------------
`consumers`中每一项的配置字段说明如下：

| 名称     | 数据类型 | 填写要求 | 默认值 | 描述                                |
| -------- | -------- | -------- | ------ | ----------------------------------- |                                                      |
| `host` | string          | 必填     | -      | 服务部署地址 |
|`auth_api` |string |必填  |- | 鉴权处理接口 |



## 配置示例

### 全局配置认证和路由粒度进行鉴权

在实例级别做如下插件配置:

```yaml
consumers:
  auth_api: "/auth/realms/paas/protocol/openid-connect/unauthorized"
  host: "center.higress-test.svc.cluster.local:8080"
white_interfaces:
  - "/info"
  - "/health"
  - "/verification/*"

```
# 鉴权返回值说明

认证服务返回：
- 返回 `true` 为鉴权失败
- 返回 `false` 为鉴权成功


# 流程简介

鉴权处理：
```txt
1. 启动鉴权插件，解析配置文件有效信息
2. 从网关拦截每个请求，解析请求中的访问接口
3. 检查接口是否在白名单中，如果在则直接放行
4. 如果接口不在白名单中，则发送请求到鉴权服务
5. 如果鉴权服务返回false，请求放行；如果返回true，返回错误码
```

# 请求示例

```bash
curl -H "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCIgOiAiSldUIiwia2lkIiA6ICJGOHJzQXNKcTVqc2JNd0M2RzgydGpMaDRBMDBPR3Vtem43dUZJOV96SXI4In0.eyJleHAiOjE3Mjg5MTE4NzUsImlhdCI6MTcyODg3NTg3NSwianRpIjoiYThkYWEyNzQtMzc5Ny00ZDMzLWIyYzAtNzdlODc4NDE0ZDcwIiwiaXNzIjoiaHR0cDovLzE5Mi4xNjguMTAyLjE4OjgwODgvYXV0aC9yZWFsbXMvcGFhcyIsInN1YiI6ImY6ZjAzNGUyMTktZjdiMi00ZTQzLTlmM2YtZTAyZDk0MDczZTc0OjI5IiwidHlwIjoiQmVhcmVyIiwiYXpwIjoiaG9sZGVyLXBjIiwic2Vzc2lvbl9zdGF0ZSI6IjFlZGI0MWMxLTk1ZTgtNGQzMy1hYTYzLWNiOTY2MDFmMmY5ZSIsImFjciI6InFyY29kZSIsImFsbG93ZWQtb3JpZ2lucyI6WyIqIl0sInNjb3BlIjoiZW1haWwgcHJvZmlsZSIsInNpZCI6IjFlZGI0MWMxLTk1ZTgtNGQzMy1hYTYzLWNiOTY2MDFmMmY5ZSIsImVtYWlsX3ZlcmlmaWVkIjpmYWxzZSwibmFtZSI6IuW8oOeoiyIsInByZWZlcnJlZF91c2VybmFtZSI6IjEzODA4MDUyMTQ0IiwiZ2l2ZW5fbmFtZSI6IuW8oOeoiyIsImVtYWlsIjoiIn0.TIe8YIaHDwo18ODSXz2ymXCHokNNCQ9AZMfTdCDbTccL8VTdb4agiR0vqkuOztTGRVyiTOHh1X7kPnvm0Kh9GLqcXaETcNhoAnSYFa6ucZWbVwCbYW21XJ-PB3Wpdtg_cr-uDPxrI8QaFYKBFfoZQ3q8o55Fx9_bMDYsnffEpTNlcE0TMcJXR7I3e-0F964wRQe9Y1k5g2OETr2bau9eDLAfGssc8QPP0zZB3xd3F-lm_op0JX1UnDf6K-ocpAzYbFYDorEQ1PVUnhVIje64V2b4nbiUEZDpaF4Go0oW9V8FUaRzHfkSNj82yPVIykydWV7TedkmzXB2HHHTZoXuZg" http://v2.192.168.100.243.nip.io/info
```
 转发到验证服务示例
```bash
curl http://center.higress-test.svc.cluster.local:8080/auth/realms/paas/protocol/openid-connect/unauthorized/eyJhbGciOiJSUzI1NiIsInR5cCIgOiAiSldUIiwia2lkIiA6ICJGOHJzQXNKcTVqc2JNd0M2RzgydGpMaDRBMDBPR3Vtem43dUZJOV96SXI4In0.eyJleHAiOjE3Mjg5MTE4NzUsImlhdCI6MTcyODg3NTg3NSwianRpIjoiYThkYWEyNzQtMzc5Ny00ZDMzLWIyYzAtNzdlODc4NDE0ZDcwIiwiaXNzIjoiaHR0cDovLzE5Mi4xNjguMTAyLjE4OjgwODgvYXV0aC9yZWFsbXMvcGFhcyIsInN1YiI6ImY6ZjAzNGUyMTktZjdiMi00ZTQzLTlmM2YtZTAyZDk0MDczZTc0OjI5IiwidHlwIjoiQmVhcmVyIiwiYXpwIjoiaG9sZGVyLXBjIiwic2Vzc2lvbl9zdGF0ZSI6IjFlZGI0MWMxLTk1ZTgtNGQzMy1hYTYzLWNiOTY2MDFmMmY5ZSIsImFjciI6InFyY29kZSIsImFsbG93ZWQtb3JpZ2lucyI6WyIqIl0sInNjb3BlIjoiZW1haWwgcHJvZmlsZSIsInNpZCI6IjFlZGI0MWMxLTk1ZTgtNGQzMy1hYTYzLWNiOTY2MDFmMmY5ZSIsImVtYWlsX3ZlcmlmaWVkIjpmYWxzZSwibmFtZSI6IuW8oOeoiyIsInByZWZlcnJlZF91c2VybmFtZSI6IjEzODA4MDUyMTQ0IiwiZ2l2ZW5fbmFtZSI6IuW8oOeoiyIsImVtYWlsIjoiIn0.TIe8YIaHDwo18ODSXz2ymXCHokNNCQ9AZMfTdCDbTccL8VTdb4agiR0vqkuOztTGRVyiTOHh1X7kPnvm0Kh9GLqcXaETcNhoAnSYFa6ucZWbVwCbYW21XJ-PB3Wpdtg_cr-uDPxrI8QaFYKBFfoZQ3q8o55Fx9_bMDYsnffEpTNlcE0TMcJXR7I3e-0F964wRQe9Y1k5g2OETr2bau9eDLAfGssc8QPP0zZB3xd3F-lm_op0JX1UnDf6K-ocpAzYbFYDorEQ1PVUnhVIje64V2b4nbiUEZDpaF4Go0oW9V8FUaRzHfkSNj82yPVIykydWV7TedkmzXB2HHHTZoXuZg
```

# 响应错误结果

```json
{
    "code": 403,
    "message": "Forbidden: Access denied"
}
```

# 响应正常

```json
{
    "code": 200,
    "message": "true"
}
```

# 相关错误码

| HTTP 状态码 | 出错信息               | 原因说明                                                                         |
| :-----------: | :----------------------: | :--------------------------------------------------------------------------------: |
| 401         | Token missing or invalid | token缺失或无效                                        |                                               |
| 403         | Forbidden: Access denied | 接口在黑名单中或鉴权失败                         |
| 500         | Auth service call failed | 调用认证服务失败 |
| 500         | Internal error: no response body | 认证服务返回空响应体|

# 技术实现

## 架构特点

- **Go语言实现**：使用Go语言重构，提供更好的性能和可维护性
- **并发安全**：使用读写锁保证并发安全
- **正则表达式支持**：支持通配符匹配和精确匹配
- **HTTP客户端**：内置HTTP客户端用于调用外部认证服务
- **错误处理**：完善的错误处理和响应机制

## 核心功能

1. **白名单/黑名单管理**：支持精确匹配和通配符匹配
2. **Token提取**：从Authorization头中提取Bearer token
3. **外部认证**：调用外部认证服务进行token验证
4. **响应处理**：根据认证结果返回相应的HTTP状态码和错误信息

## 性能优化

- **正则表达式预编译**：在初始化时编译正则表达式，提高运行时性能
- **内存管理**：使用map存储精确匹配项，提高查找效率
- **连接池**：HTTP客户端支持连接复用
- **超时控制**：设置合理的超时时间，避免长时间等待