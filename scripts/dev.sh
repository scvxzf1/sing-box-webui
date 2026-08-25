#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -eq 0 ]]; then
  printf 'error: development services must not run as root\n' >&2
  exit 1
fi

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export SING_BOX_WEBUI_ADDR="${SING_BOX_WEBUI_ADDR:-127.0.0.1:33334}"
export SING_BOX_WEBUI_DEV_ORIGIN="${SING_BOX_WEBUI_DEV_ORIGIN:-http://127.0.0.1:33333}"
export SING_BOX_WEBUI_DEV_API="${SING_BOX_WEBUI_DEV_API:-http://${SING_BOX_WEBUI_ADDR}}"

for command in curl go npm setsid; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done

if [[ ! -d "${ROOT_DIR}/web/node_modules" ]]; then
  printf 'error: frontend dependencies are missing; run npm --prefix web install\n' >&2
  exit 1
fi

backend_pid=''
frontend_pid=''

cleanup() {
  trap - EXIT INT TERM
  for pid in "${backend_pid}" "${frontend_pid}"; do
    if [[ -n "${pid}" ]]; then
      kill -TERM -- "-${pid}" 2>/dev/null || true
    fi
  done
  wait "${backend_pid}" "${frontend_pid}" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

printf 'Go API: http://%s\n' "${SING_BOX_WEBUI_ADDR}"
printf 'WebUI:  http://127.0.0.1:33333\n'

(
  cd "${ROOT_DIR}"
  exec setsid go run ./cmd/webui
) &
backend_pid=$!

backend_ready=false
for _ in {1..300}; do
  if curl --fail --silent --show-error --max-time 1 "http://${SING_BOX_WEBUI_ADDR}/healthz" >/dev/null 2>&1; then
    backend_ready=true
    break
  fi
  if ! kill -0 "${backend_pid}" 2>/dev/null; then
    wait "${backend_pid}" || true
    printf 'error: Go API exited before becoming ready\n' >&2
    exit 1
  fi
  sleep 0.1
done

if [[ "${backend_ready}" != true ]]; then
  printf 'error: Go API did not become ready within 30 seconds\n' >&2
  exit 1
fi

(
  cd "${ROOT_DIR}"
  exec setsid npm --prefix web run dev
) &
frontend_pid=$!

wait -n "${backend_pid}" "${frontend_pid}"
