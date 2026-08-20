#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -eq 0 ]]; then
  printf 'error: do not run the WebUI as root; only the sing-box core needs TUN capability\n' >&2
  exit 1
fi

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MANAGED_LINK="${SING_BOX_WEBUI_DATA_DIR:-${ROOT_DIR}/var/data}/core/sing-box"
BINARY="${SING_BOX_BIN:-${MANAGED_LINK}}"

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
