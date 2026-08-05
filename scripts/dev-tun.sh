#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -eq 0 ]]; then
  printf 'error: TUN development must run as an ordinary user\n' >&2
  exit 1
fi

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
MANAGED_LINK="${SING_BOX_WEBUI_DATA_DIR:-${ROOT_DIR}/var/data}/core/sing-box"
BINARY="${SING_BOX_BIN:-${MANAGED_LINK}}"

if [[ ! -x "${BINARY}" ]]; then
  printf 'error: sing-box executable not found: %s\n' "${BINARY}" >&2
  printf 'start ./scripts/dev.sh once to install the managed core, then retry\n' >&2
  exit 1
fi

if ! command -v getcap >/dev/null 2>&1; then
  printf 'error: getcap is required to verify TUN permissions\n' >&2
  exit 1
fi

RESOLVED_BINARY="$(readlink -f -- "${BINARY}")"
CAPABILITIES="$(getcap -- "${RESOLVED_BINARY}" 2>/dev/null || true)"
if [[ "${CAPABILITIES}" != *cap_net_admin* ]]; then
  printf 'error: CAP_NET_ADMIN is missing from %s\n' "${RESOLVED_BINARY}" >&2
  printf 'grant it once with:\n  sudo setcap cap_net_admin+ep %q\n' "${RESOLVED_BINARY}" >&2
  exit 1
fi

export SING_BOX_WEBUI_ENABLE_TUN=1
exec "${ROOT_DIR}/scripts/dev.sh"
