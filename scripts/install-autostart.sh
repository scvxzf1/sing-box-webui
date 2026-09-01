#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -eq 0 ]]; then
  printf 'error: install the user service as the normal project user, not root\n' >&2
  exit 1
fi

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="${SING_BOX_WEBUI_SERVICE_NAME:-sing-box-webui-dev.service}"
CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
SERVICE_DIR="${CONFIG_HOME}/systemd/user"
SERVICE_PATH="${SERVICE_DIR}/${SERVICE_NAME}"
START_NOW=true
DEV_PORT="${SING_BOX_WEBUI_DEV_PORT:-31333}"

if [[ ! "${DEV_PORT}" =~ ^[1-9][0-9]{0,4}$ ]] || (( DEV_PORT > 65535 )); then
  printf 'error: SING_BOX_WEBUI_DEV_PORT must be between 1 and 65535\n' >&2
  exit 1
fi

case "${1:-}" in
  '') ;;
  --no-start)
    START_NOW=false
    ;;
  --help|-h)
    printf 'usage: %s [--no-start]\n' "${BASH_SOURCE[0]}"
    printf '  --no-start  enable the unit without starting it immediately\n'
    exit 0
    ;;
  *)
    printf 'error: unknown option: %s\n' "$1" >&2
    printf 'usage: %s [--no-start]\n' "${BASH_SOURCE[0]}" >&2
    exit 2
    ;;
esac

if [[ ! "${SERVICE_NAME}" =~ ^[A-Za-z0-9_.@-]+\.service$ ]]; then
  printf 'error: invalid user service name: %s\n' "${SERVICE_NAME}" >&2
  exit 1
fi

for command in getcap readlink systemctl; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done

NODE_BIN="${SING_BOX_WEBUI_NODE_BIN:-$(command -v node || true)}"
NPM_BIN="${SING_BOX_WEBUI_NPM_BIN:-$(command -v npm || true)}"
GO_BIN="${SING_BOX_WEBUI_GO_BIN:-$(command -v go || true)}"
for value in NODE_BIN NPM_BIN GO_BIN; do
  if [[ -z "${!value}" || ! -x "${!value}" ]]; then
    printf 'error: %s is required in the current shell PATH\n' "${value%_BIN}" >&2
    exit 1
  fi
done

if [[ ! -d "${ROOT_DIR}/web/node_modules" ]]; then
  printf 'error: frontend dependencies are missing: %s/web/node_modules\n' "${ROOT_DIR}/web" >&2
  printf 'run npm --prefix web install, then retry\n' >&2
  exit 1
fi

CORE_LINK="${SING_BOX_WEBUI_DATA_DIR:-${ROOT_DIR}/var/data}/core/sing-box"
if [[ ! -e "${CORE_LINK}" ]]; then
  printf 'error: sing-box core not found: %s\n' "${CORE_LINK}" >&2
  printf 'start ./scripts/dev.sh once to install the managed core, then retry\n' >&2
  exit 1
fi

RESOLVED_CORE="$(readlink -f -- "${CORE_LINK}")"
CAPABILITIES="$(getcap -- "${RESOLVED_CORE}" 2>/dev/null || true)"
if [[ "${CAPABILITIES}" != *cap_net_admin* ]]; then
  printf 'error: CAP_NET_ADMIN is missing from %s\n' "${RESOLVED_CORE}" >&2
  printf 'grant it once, then retry:\n  sudo setcap cap_net_admin+ep %q\n' "${RESOLVED_CORE}" >&2
  exit 1
fi

if ! systemctl --user show-environment >/dev/null 2>&1; then
  printf 'error: the systemd user manager is unavailable\n' >&2
  printf 'log in to the target user session and retry\n' >&2
  exit 1
fi

NODE_DIR="$(dirname -- "${NODE_BIN}")"
NPM_DIR="$(dirname -- "${NPM_BIN}")"
GO_DIR="$(dirname -- "${GO_BIN}")"
SERVICE_PATH_VALUE="${NODE_DIR}:${NPM_DIR}:${GO_DIR}:${HOME}/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

mkdir -p -- "${SERVICE_DIR}"
TEMP_PATH="$(mktemp "${SERVICE_PATH}.tmp.XXXXXX")"
cleanup() {
  rm -f -- "${TEMP_PATH}"
}
trap cleanup EXIT

cat >"${TEMP_PATH}" <<EOF
[Unit]
Description=sing-box WebUI development service with TUN support
StartLimitIntervalSec=60s
StartLimitBurst=5

[Service]
Type=simple
WorkingDirectory=${ROOT_DIR}
Environment="PATH=${SERVICE_PATH_VALUE}"
Environment=SING_BOX_WEBUI_ENABLE_TUN=1
Environment=SING_BOX_WEBUI_DEV_PORT=${DEV_PORT}
Environment=SING_BOX_WEBUI_ADDR=127.0.0.1:31334
ExecStart=${ROOT_DIR}/scripts/dev-tun.sh
Restart=on-failure
RestartSec=5s
KillMode=control-group
TimeoutStopSec=20s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
EOF

chmod 0644 "${TEMP_PATH}"
mv -- "${TEMP_PATH}" "${SERVICE_PATH}"
trap - EXIT

systemctl --user daemon-reload
systemctl --user enable "${SERVICE_NAME}" >/dev/null
if [[ "${START_NOW}" == true ]]; then
  if systemctl --user is-active --quiet "${SERVICE_NAME}"; then
    systemctl --user restart "${SERVICE_NAME}"
  else
    systemctl --user start "${SERVICE_NAME}"
  fi
fi

if [[ "${START_NOW}" == true ]]; then
  printf 'enabled and started: %s\n' "${SERVICE_NAME}"
else
  printf 'enabled (not started): %s\n' "${SERVICE_NAME}"
fi
printf 'unit: %s\n' "${SERVICE_PATH}"
printf 'logs: journalctl --user -u %s -f\n' "${SERVICE_NAME}"
