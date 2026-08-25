#!/usr/bin/env bash
# 停止 sing-box-webui：nginx 容器 + 后端进程
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

# 1. 停止 nginx 容器
/usr/bin/sg docker -c "docker compose -f $SCRIPT_DIR/docker-compose.yml --project-directory $SCRIPT_DIR down" 2>/dev/null || true

# 2. 停止后端进程
if [ -f "$ROOT_DIR/var/run/webui.pid" ]; then
  pid="$(cat "$ROOT_DIR/var/run/webui.pid")"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    sleep 1
    kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null || true
    echo "[webui] backend stopped (pid $pid)"
  fi
  rm -f "$ROOT_DIR/var/run/webui.pid"
fi

echo "[webui] stopped"
