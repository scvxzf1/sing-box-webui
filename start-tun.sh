#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -eq 0 ]]; then
  printf 'error: do not run the WebUI as root; only the sing-box core needs TUN capability\n' >&2
  exit 1
fi

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEV_PORT="${SING_BOX_WEBUI_DEV_PORT:-31333}"
API_ADDRESS="${SING_BOX_WEBUI_ADDR:-127.0.0.1:31334}"
MANAGED_LINK="${SING_BOX_WEBUI_DATA_DIR:-${ROOT_DIR}/var/data}/core/sing-box"
BINARY="${SING_BOX_BIN:-${MANAGED_LINK}}"

if command -v curl >/dev/null 2>&1; then
  API_STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 2 \
    "http://${API_ADDRESS}/api/v1/runtime" 2>/dev/null || true)"
  if [[ "${API_STATUS}" == "200" || "${API_STATUS}" == "401" ]]; then
    printf 'sing-box WebUI is already running\n'
    printf 'WebUI: http://127.0.0.1:%s\n' "${DEV_PORT}"
    exit 0
  fi
fi

if [[ ! -x "${BINARY}" ]]; then
  printf 'error: sing-box executable not found: %s\n' "${BINARY}" >&2
  printf 'start ./scripts/dev.sh once to install the managed core, then retry\n' >&2
  exit 1
fi

for command in getcap readlink; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done

RESOLVED_BINARY="$(readlink -f -- "${BINARY}")"
CAPABILITIES="$(getcap -- "${RESOLVED_BINARY}" 2>/dev/null || true)"
if [[ "${CAPABILITIES}" != *cap_net_admin* ]]; then
  for command in setcap sudo; do
    if ! command -v "${command}" >/dev/null 2>&1; then
      printf 'error: required command not found: %s\n' "${command}" >&2
      exit 1
    fi
  done
  printf 'granting CAP_NET_ADMIN to %s (administrator password may be requested)\n' "${RESOLVED_BINARY}"
  if ! sudo setcap cap_net_admin+ep "${RESOLVED_BINARY}"; then
    printf 'error: failed to grant CAP_NET_ADMIN to %s\n' "${RESOLVED_BINARY}" >&2
    exit 1
  fi
fi

export SING_BOX_WEBUI_ENABLE_TUN=1
exec "${ROOT_DIR}/scripts/dev-tun.sh" "$@"
