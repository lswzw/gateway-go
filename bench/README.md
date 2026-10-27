# bench — 真实请求压测

Docker 隔离 + [`hey`](https://github.com/rakyll/hey) 的压测脚本，用于复现
[性能测试报告](../docs/performance-testing.md) 中的数据。

## 隔离方式

| 容器 | CPU | 内存 | 作用 |
|---|---|---|---|
| `gwbench-gw` | 0-3 | 2 GiB | 被测网关（静态编译二进制 + `bench.yaml`） |
| `gwbench-backend` | 4-7 | 2 GiB | `nginx:stable-alpine`，`access_log off`，固定返回 `backend-ok` |
| `gwbench-load` | 8-11 | 4 GiB | `hey`，只负责发压 |

三组容器绑定不同 CPU 核，避免互相抢核心；但内存带宽/L3 仍共享，结果用于**相对比较**，
不能直接当作绝对容量承诺。

## 前置条件

- Docker（能起 `alpine:3.22`、`nginx:stable-alpine`）
- Go 1.26+（构建网关与 `hey`）
- 宿主机至少 12 核（需要三组 cpuset），否则请自行调整 `run.sh` 里的 `--cpuset-cpus`

## 用法

```bash
./bench/run.sh up        # 构建二进制、建网络、起后端与压测容器
./bench/run.sh matrix    # 100/200/400 × direct/self/noplug/plug
./bench/run.sh hiconc    # 800/1600 × 默认参数 / 内核调优（tcp_tw_reuse + 扩大端口范围）
./bench/run.sh soak      # 180s @ c=100，每 15s 采样 RSS/线程/CPU
./bench/run.sh reload    # 带载 SIGHUP，验证重载期间请求是否中断
./bench/run.sh down
```

可用环境变量：`LEVELS`、`SCENARIOS`、`WARM`（预热秒数，默认 5）、`DUR`（计时秒数，默认 25）、
`OUT`（结果目录）、`HEY`（已有 hey 的路径）。原始输出（hey 摘要 + 每例结果）写入 `OUT`。

## 口径

- 每个用例前**重启网关与后端容器**：清空 netns 内残留的 `TIME_WAIT` 与限流令牌桶，
  否则上一轮的端口耗尽会把下一轮数据带崩。
- 先预热再计时，延迟取 p50/p90/p99。
- 需要交叉校验时，用管理端口 `/metrics` 的 `gateway_requests_total` 增量与 hey 的 RPS 比对。

## 注意

- `bench/bin/`（编译产物）不入库，已加入 `.gitignore`。
- 压测不接 CI：需要独占 CPU 与稳定网络，跑在流水线里既慢又会污染结果。
