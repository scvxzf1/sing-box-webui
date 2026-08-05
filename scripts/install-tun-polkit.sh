#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RULE_SOURCE="${ROOT_DIR}/deploy/polkit/49-sing-box-webui-resolved.rules"
RULE_TARGET="/etc/polkit-1/rules.d/49-sing-box-webui-resolved.rules"
CURRENT_USER="$(id -un)"
TEMP_RULE=''

cleanup() {
	if [[ -n "${TEMP_RULE}" ]]; then
		rm -f -- "${TEMP_RULE}"
	fi
}
trap cleanup EXIT

if [[ ! "${CURRENT_USER}" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]]; then
	printf 'error: unsupported local user name: %s\n' "${CURRENT_USER}" >&2
	exit 1
fi
if [[ ! -r "${RULE_SOURCE}" ]]; then
  printf 'error: policy source is missing: %s\n' "${RULE_SOURCE}" >&2
  exit 1
fi
if [[ ! -x /usr/bin/pkexec || ! -x /usr/bin/install ]]; then
  printf 'error: pkexec and install are required\n' >&2
  exit 1
fi

printf 'Installing the restricted sing-box WebUI DNS policy.\n'
printf 'One administrator authentication is required; later TUN toggles will not prompt.\n'
TEMP_RULE="$(mktemp)"
sed "s/__SING_BOX_WEBUI_USER__/${CURRENT_USER}/g" "${RULE_SOURCE}" >"${TEMP_RULE}"
/usr/bin/pkexec /usr/bin/install \
	--owner=root --group=root --mode=0644 \
	"${TEMP_RULE}" "${RULE_TARGET}"
