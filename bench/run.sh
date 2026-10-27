#!/usr/bin/env bash
#
# 真实请求压测入口：docker 隔离（网关 4C / 后端 4C / 压测端 4C）+ hey。
# 用法：
#   ./bench/run.sh up        # 构建二进制、建网络、起后端与压测容器
#   ./bench/run.sh matrix    # 基准矩阵：100/200/400 × direct/self/noplug/plug
#   ./bench/run.sh hiconc    # 高并发：800/1600 × 默认参数/内核调优
#   ./bench/run.sh soak      # 长稳：180s @ c=100
#   ./bench/run.sh reload    # 热重载可用性（SIGHUP 期间请求是否中断）
#   ./bench/run.sh down      # 停止并删除容器与网络
#
# 可用环境变量：LEVELS / SCENARIOS / WARM / DUR / OUT / HEY
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$BENCH_DIR/.." && pwd)"
NET=gwbench
GW=gwbench-gw
BACKEND=gwbench-backend
LOAD=gwbench-load

LEVELS="${LEVELS:-100 200 400}"
SCENARIOS="${SCENARIOS:-direct self noplug plug}"
WARM="${WARM:-5}"
DUR="${DUR:-25}"
OUT="${OUT:-$BENCH_DIR/results/$(date +%Y%m%d-%H%M%S)}"
HEY="${HEY:-$(command -v hey || true)}"
GW_BIN="$BENCH_DIR/bin/gateway"

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

build() {
  mkdir -p "$BENCH_DIR/bin"
  log "构建网关静态二进制"
  ( cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$GW_BIN" ./cmd/gateway )
  if [ -z "$HEY" ] || [ ! -x "$HEY" ]; then
    log "构建 hey"
    ( cd "$REPO_DIR" && CGO_ENABLED=0 GOBIN="$BENCH_DIR/bin" go install github.com/rakyll/hey@v0.1.4 )
    HEY="$BENCH_DIR/bin/hey"
  fi
}

need_hey() {
  if [ -z "$HEY" ] || [ ! -x "$HEY" ]; then
    echo "未找到 hey，请先执行 ./bench/run.sh up" >&2
    exit 1
  fi
}

up() {
  build
  docker network create --driver bridge --subnet 172.31.99.0/24 "$NET" >/dev/null 2>&1 || true
  docker rm -f "$BACKEND" "$LOAD" >/dev/null 2>&1 || true
  docker run -d --name "$BACKEND" --network "$NET" --network-alias backend \
    --cpuset-cpus 4-7 --memory 2g --ulimit nofile=200000:200000 \
    -v "$BENCH_DIR/nginx.conf:/etc/nginx/nginx.conf:ro" nginx:stable-alpine >/dev/null
  docker run -d --name "$LOAD" --network "$NET" --cpuset-cpus 8-11 --memory 4g \
    --ulimit nofile=200000:200000 --entrypoint /bin/sleep \
    -v "$HEY:/hey:ro" alpine:3.22 infinity >/dev/null
  log "环境就绪（后端 $BACKEND，压测端 $LOAD）"
}

start_gw() { # 额外参数传给 docker run（如 --sysctl）
  docker rm -f "$GW" >/dev/null 2>&1 || true
  docker run -d --name "$GW" --network "$NET" --cpuset-cpus 0-3 --memory 2g \
    --ulimit nofile=200000:200000 "$@" \
    -v "$GW_BIN:/gateway:ro" -v "$BENCH_DIR/bench.yaml:/config/config.yaml:ro" \
    --workdir / --entrypoint /gateway alpine:3.22 -c /config/config.yaml >/dev/null
  for _ in $(seq 1 20); do
    if docker exec "$LOAD" /bin/sh -c 'wget -qO- --timeout=1 http://gwbench-gw:9090/readyz' 2>/dev/null | grep -q ready; then
      return 0
    fi
    sleep 0.5
  done
  log "网关未就绪"; return 1
}

tw_count() {
  local pid; pid=$(docker inspect -f '{{.State.Pid}}' "$GW" 2>/dev/null || echo "")
  [ -z "$pid" ] && { echo 0; return; }
  awk 'NR>1 && $4=="06"' "/proc/$pid/net/tcp" 2>/dev/null | wc -l | tr -d ' '
}

hey_run() { # $1=并发 $2=时长 $3=url $4=输出文件
  docker exec "$LOAD" /hey -z "$2" -c "$1" "$3" > "$4" 2>&1 || true
}

report() { # $1=label $2=file
  printf '%-22s ' "$1"
  grep -E 'Requests/sec' "$2" | tr '\n' ' ' || true
  awk '/ 50% in| 90% in| 99% in/{printf "%s ", $3}' "$2" || true
  grep -E '\[[0-9]{3}\]' "$2" | tr '\n' ' ' || true
  echo
}

url_of() {
  case "$1" in
    direct) echo "http://$BACKEND:80/" ;;
    self)   echo "http://$GW:8080/" ;;
    noplug) echo "http://$GW:8080/noplug" ;;
    plug)   echo "http://$GW:8080/plug" ;;
    *) echo "unknown scenario: $1" >&2; exit 1 ;;
  esac
}

matrix() {
  need_hey
  mkdir -p "$OUT"
  for c in $LEVELS; do
    for s in $SCENARIOS; do
      docker restart "$BACKEND" >/dev/null 2>&1 || true
      start_gw >/dev/null
      hey_run "$c" "${WARM}s" "$(url_of "$s")" /dev/null
      log "measure $s c=$c"
      hey_run "$c" "${DUR}s" "$(url_of "$s")" "$OUT/${s}_c${c}.txt"
      report "$s c=$c" "$OUT/${s}_c${c}.txt"
      printf '  TIME_WAIT=%s\n' "$(tw_count)"
    done
  done
}

hiconc() {
  need_hey
  mkdir -p "$OUT"
  for c in ${LEVELS:-800 1600}; do
    for s in noplug plug; do
      docker restart "$BACKEND" >/dev/null 2>&1 || true
      start_gw >/dev/null
      hey_run "$c" "${WARM}s" "$(url_of "$s")" /dev/null
      hey_run "$c" "${DUR}s" "$(url_of "$s")" "$OUT/default_${s}_c${c}.txt"
      report "default $s c=$c" "$OUT/default_${s}_c${c}.txt"

      docker restart "$BACKEND" >/dev/null 2>&1 || true
      start_gw --sysctl net.ipv4.tcp_tw_reuse=1 --sysctl "net.ipv4.ip_local_port_range=10000 65535" >/dev/null
      hey_run "$c" "${WARM}s" "$(url_of "$s")" /dev/null
      hey_run "$c" "${DUR}s" "$(url_of "$s")" "$OUT/tuned_${s}_c${c}.txt"
      report "tuned   $s c=$c" "$OUT/tuned_${s}_c${c}.txt"
    done
  done
}

soak() {
  need_hey
  mkdir -p "$OUT"
  local conc="${CONC:-100}" dur="${SOAK_DUR:-180}"
  start_gw >/dev/null
  pid=$(docker inspect -f '{{.State.Pid}}' "$GW")
  ( hey_run "$conc" "${dur}s" "$(url_of noplug)" "$OUT/soak_hey.txt" ) &
  local heypid=$!
  for i in $(seq 1 $((dur / 15))); do
    sleep 15
    printf 't=%ss rss=%skB threads=%s cpu=%s\n' "$((i * 15))" \
      "$(awk '/VmRSS/{print $2}' "/proc/$pid/status")" \
      "$(awk '/Threads/{print $2}' "/proc/$pid/status")" \
      "$(docker stats --no-stream --format '{{.CPUPerc}}' "$GW")"
  done
  wait "$heypid"
  report "soak c=$conc" "$OUT/soak_hey.txt"
}

reload() {
  need_hey
  start_gw >/dev/null
  probe() {
    docker exec "$LOAD" /bin/sh -c 'wget -q -O /dev/null --timeout=3 http://gwbench-gw:8080/noplug; echo rc=$?' 2>&1 | tr '\n' ' '
  }
  log "重载前: $(probe)"
  ( hey_run 50 "40s" "$(url_of noplug)" "$OUT/reload_load.txt" ) &
  local heypid=$!
  sleep 10
  log "发送 SIGHUP"
  docker kill -s HUP "$GW" >/dev/null
  for i in 1 2 3 4 5 6; do sleep 4; log "+$((i * 4))s: $(probe)"; done
  wait "$heypid"
  report "reload c=50" "$OUT/reload_load.txt"
  docker logs "$GW" 2>&1 | tail -4
}

down() {
  docker rm -f "$GW" "$BACKEND" "$LOAD" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  log "已清理"
}

case "${1:-}" in
  up) up ;;
  matrix) matrix ;;
  hiconc) hiconc ;;
  soak) soak ;;
  reload) reload ;;
  down) down ;;
  *) sed -n '2,14p' "$0"; exit 1 ;;
esac
