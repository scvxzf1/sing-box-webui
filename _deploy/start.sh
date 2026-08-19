#!/usr/bin/env bash
# 启动 sing-box-webui：后端（普通用户）+ nginx 反代
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export SING_BOX_WEBUI_ADDR="${SING_BOX_WEBUI_ADDR:-127.0.0.1:33334}"
export SING_BOX_WEBUI_DEV_ORIGIN="${SING_BOX_WEBUI_DEV_ORIGIN:-http://127.0.0.1:33334}"
export SING_BOX_WEBUI_DATA_DIR="${SING_BOX_WEBUI_DATA_DIR:-$ROOT_DIR/var/data}"
export SING_BOX_WEBUI_RUNTIME_DIR="${SING_BOX_WEBUI_RUNTIME_DIR:-$ROOT_DIR/var/run}"
export SING_BOX_WEBUI_CONFIG="${SING_BOX_WEBUI_CONFIG:-$ROOT_DIR/var/config.json}"
export SING_BOX_WEBUI_ENABLE_TUN=1
export SING_BOX_WEBUI_LOG_LEVEL="${SING_BOX_WEBUI_LOG_LEVEL:-info}"

mkdir -p "$ROOT_DIR/var/data" "$ROOT_DIR/var/run" "$ROOT_DIR/var/logs"

# 0. TUN 能力检查：确保 sing-box 核心有 CAP_NET_ADMIN
if [ "${SING_BOX_WEBUI_ENABLE_TUN:-0}" = "1" ]; then
  CORE_BIN="$ROOT_DIR/var/data/core/sing-box"
  if [ -x "$CORE_BIN" ]; then
    RESOLVED_BIN="$(readlink -f "$CORE_BIN")"
    # 用 docker 特权容器检查并修复 capability
    CAP_OK=$(docker run --rm --privileged --pid=host -v /:/host debian:latest bash -c \
      "chroot /host /sbin/getcap '$RESOLVED_BIN' 2>/dev/null" 2>/dev/null || true)
    if echo "$CAP_OK" | grep -qv 'cap_net_admin'; then
      echo "[webui] granting CAP_NET_ADMIN to sing-box core ..."
      docker run --rm --privileged --pid=host -v /:/host debian:latest bash -c \
        "chroot /host /sbin/setcap cap_net_admin+ep '$RESOLVED_BIN'" 2>/dev/null || true
      echo "[webui] CAP_NET_ADMIN granted"
    fi
  fi
fi

# 1. 启动 Go 后端（普通用户，监听回环）
if ! ss -tlnp 2>/dev/null | grep -q "127.0.0.1:33334"; then
  echo "[webui] starting backend on $SING_BOX_WEBUI_ADDR ..."
  nohup "$ROOT_DIR/bin/sing-box-webui" \
    > "$ROOT_DIR/var/logs/webui.log" 2>&1 &
  echo $! > "$ROOT_DIR/var/run/webui.pid"
  # 等待后端就绪
  for i in $(seq 1 20); do
    if curl -s -m 1 http://127.0.0.1:33334/healthz >/dev/null 2>&1; then
      echo "[webui] backend ready (pid $(cat "$ROOT_DIR/var/run/webui.pid"))"
      break
    fi
    sleep 0.3
  done
else
  echo "[webui] backend already running on 127.0.0.1:33334"
fi

# 2. 启动 nginx 反代容器（监听 9909）
echo "[webui] starting nginx reverse proxy on 0.0.0.0:9909 ..."
/usr/bin/sg docker -c "docker compose -f $ROOT_DIR/docker-compose.yml --project-directory $ROOT_DIR up -d --remove-orphans"

echo "[webui] done. UI: http://192.168.5.149:9909/"
