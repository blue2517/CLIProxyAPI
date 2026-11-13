#!/usr/bin/env bash
# CPA & CPA Manager Plus Updater
# Usage: bash /opt/cliproxyapi/update.sh [--yes|-y]
# Check versions, ask for confirmation, then update. No backups are created.
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

AUTO_YES=false
case "${1:-}" in
    --yes|-y) AUTO_YES=true ;;
    '') ;;
    *) echo "Usage: bash $0 [--yes|-y]" >&2; exit 2 ;;
esac
[[ $# -le 1 ]] || { echo "Usage: bash $0 [--yes|-y]" >&2; exit 2; }

# Keep the existing CPA fork and installation.
CPA_REPO_OWNER="blue2517"
CPA_REPO_NAME="CLIProxyAPI"
CPA_INSTALL_DIR="${CPA_INSTALL_DIR:-/opt/cliproxyapi}"
CPA_SERVICE_NAME="cliproxyapi"
CPA_BINARY_NAME="cli-proxy-api"
CPA_API_URL="https://api.github.com/repos/${CPA_REPO_OWNER}/${CPA_REPO_NAME}/releases/latest"

CPAMP_REPO_OWNER="blue2517"
CPAMP_REPO_NAME="CPA-Manager-Plus"
CPAMP_INSTALL_DIR="${CPAMP_INSTALL_DIR:-/opt/cpa-manager-plus}"
CPAMP_SERVICE_NAME="cpa-manager-plus"
CPAMP_BINARY_NAME="cpa-manager-plus"
CPAMP_API_URL="https://api.github.com/repos/${CPAMP_REPO_OWNER}/${CPAMP_REPO_NAME}/releases/latest"

for command in curl jq tar systemctl timeout sha256sum awk install; do
    command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }
done

detect_arch() {
    case "$(uname -m)" in
        x86_64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) echo "Unsupported architecture: $(uname -m)" >&2; return 1 ;;
    esac
}

get_current_version() {
    if [[ -f "$1/version.txt" ]]; then
        tr -d '[:space:]' < "$1/version.txt"
    else
        echo unknown
    fi
}

get_latest_release() {
    curl -fsSL --connect-timeout 10 --max-time 30 --retry 2 "$1"
}

parse_tag() {
    local tag
    tag=$(jq -er '.tag_name' <<< "$1") || return 1
    [[ "$tag" =~ ^v?[0-9]+(\.[0-9]+)+([+-][0-9A-Za-z.-]+)?$ ]] || return 1
    printf '%s\n' "$tag"
}

asset_url() {
    jq -er --arg name "$2" '[.assets[] | select(.name == $name)] |
        if length == 1 then .[0].browser_download_url else error("Release asset missing") end' <<< "$1"
}

download() {
    if ! curl -fsSL --connect-timeout 10 --max-time 180 --retry 3 --retry-delay 2 -o "$2" "$1"; then
        echo -e "${YELLOW}curl failed, falling back to wget...${NC}"
        command -v wget >/dev/null && wget --tries=3 --timeout=30 -q -O "$2" "$1"
    fi
}

update_component() (
    # A subshell-scoped EXIT trap also cleans up on extraction/start failures.
    set -euo pipefail
    name=$1 install_dir=$2 service_name=$3 binary_name=$4
    asset_name=$5 download_url=$6 latest_version=$7 strip=${8:-0} checksum_url=${9:-}
    tmp_dir=$(mktemp -d)
    pending_binary="${install_dir}/.${binary_name}.new"
    trap 'rm -rf -- "$tmp_dir"; rm -f -- "$pending_binary"' EXIT

    echo ""
    echo -e "${BLUE}[${name} 1/4]${NC} Downloading ${asset_name}..."
    download "$download_url" "${tmp_dir}/${asset_name}"

    echo -e "${BLUE}[${name} 2/4]${NC} Preparing executable..."
    if [[ "$binary_name" == cpa-manager-plus ]]; then
        # This fork publishes a standalone executable, not the upstream archive.
        [[ -n "$checksum_url" ]] || { echo 'Manager checksum URL missing.' >&2; exit 1; }
        download "$checksum_url" "${tmp_dir}/checksums.txt"
        expected_hash=$(awk -v name="$asset_name" '$2 == name || $2 == "*" name { print $1 }' "${tmp_dir}/checksums.txt")
        [[ "$expected_hash" =~ ^[0-9a-fA-F]{64}$ ]] || { echo 'Manager checksum entry missing, duplicate or invalid.' >&2; exit 1; }
        (cd "$tmp_dir"; printf '%s  %s\n' "$expected_hash" "$asset_name" | sha256sum --check --status -) || {
            echo 'Manager binary checksum mismatch.' >&2; exit 1;
        }
        install -m 755 "${tmp_dir}/${asset_name}" "$pending_binary"
        package_version=$(timeout 5 "$pending_binary" --version)
        [[ "${package_version#v}" == "$latest_version" ]] || { echo 'Manager binary version mismatch.' >&2; exit 1; }
    else
        mkdir "${tmp_dir}/extracted"
        tar -xzf "${tmp_dir}/${asset_name}" -C "${tmp_dir}/extracted" --strip-components="$strip"
        [[ -f "${tmp_dir}/extracted/${binary_name}" ]] || { echo 'Binary not found in archive.' >&2; exit 1; }
        install -m 755 "${tmp_dir}/extracted/${binary_name}" "$pending_binary"
    fi

    echo -e "${BLUE}[${name} 3/4]${NC} Stopping service and replacing executable..."
    systemctl stop "$service_name"
    mv -f "$pending_binary" "${install_dir}/${binary_name}"

    echo -e "${BLUE}[${name} 4/4]${NC} Starting service..."
    systemctl start "$service_name"
    sleep 2
    systemctl is-active --quiet "$service_name"
    printf '%s\n' "$latest_version" > "${install_dir}/version.txt"
    echo -e "${GREEN}${name} updated to ${latest_version}!${NC}"
)

echo -e "${BLUE}CPA & CPA Manager Plus Updater${NC}"
echo "================================"
arch=$(detect_arch)
updates=()
check_failed=false

if [[ -x "${CPA_INSTALL_DIR}/${CPA_BINARY_NAME}" ]]; then
    cpa_current=$(get_current_version "$CPA_INSTALL_DIR")
    cpa_current=${cpa_current#v}
    echo -e "CPA current: ${YELLOW}${cpa_current}${NC}"
    if cpa_release=$(get_latest_release "$CPA_API_URL") && cpa_tag=$(parse_tag "$cpa_release"); then
        cpa_latest=${cpa_tag#v}
        echo -e "CPA latest:  ${GREEN}${cpa_latest}${NC}"
        if [[ "$cpa_current" != "$cpa_latest" ]]; then
            cpa_asset="CLIProxyAPI_${cpa_latest}_linux_${arch}.tar.gz"
            if cpa_url=$(asset_url "$cpa_release" "$cpa_asset"); then
                updates+=(cpa)
            else
                check_failed=true
            fi
        else
            echo -e "CPA ${GREEN}up to date${NC}"
        fi
    else
        echo -e "CPA ${RED}failed to fetch release info${NC}"
        check_failed=true
    fi
else
    echo -e "CPA ${YELLOW}not installed (${CPA_INSTALL_DIR})${NC}"
fi

echo ""
if [[ -d "$CPAMP_INSTALL_DIR" ]]; then
    # Keep the current WorkingDirectory and its config.json. Replace executables
    # in place, never copy release defaults over config/data/secrets or edit units.
    # Its directory name may contain an older version; the binary is authoritative.
    cpamp_runtime=$(systemctl show "$CPAMP_SERVICE_NAME" -p WorkingDirectory --value)
    if [[ "$cpamp_runtime" == "$CPAMP_INSTALL_DIR/runtime/"* && -x "$cpamp_runtime/$CPAMP_BINARY_NAME" ]]; then
        cpamp_current=$(timeout 5 "$cpamp_runtime/$CPAMP_BINARY_NAME" --version)
        cpamp_current=${cpamp_current#v}
        echo -e "CPA Manager Plus current: ${YELLOW}${cpamp_current}${NC}"
        if cpamp_release=$(get_latest_release "$CPAMP_API_URL") && cpamp_tag=$(parse_tag "$cpamp_release"); then
            cpamp_latest=${cpamp_tag#v}
            echo -e "CPA Manager Plus latest:  ${GREEN}${cpamp_latest}${NC}"
            if [[ "$cpamp_current" != "$cpamp_latest" ]]; then
                cpamp_asset="cpa-manager-plus_linux_${arch}"
                if cpamp_url=$(asset_url "$cpamp_release" "$cpamp_asset") &&
                   cpamp_checksum_url=$(asset_url "$cpamp_release" checksums.txt); then
                    updates+=(cpamp)
                else
                    check_failed=true
                fi
            else
                echo -e "CPA Manager Plus ${GREEN}up to date${NC}"
            fi
        else
            echo -e "CPA Manager Plus ${RED}failed to fetch release info${NC}"
            check_failed=true
        fi
    else
        echo -e "${RED}CPA Manager Plus service runtime was not found.${NC}"
        check_failed=true
    fi
else
    echo -e "CPA Manager Plus ${YELLOW}not installed (${CPAMP_INSTALL_DIR})${NC}"
fi

if [[ "$check_failed" == true ]]; then
    echo -e "${RED}Version checking failed. No updates applied.${NC}" >&2
    exit 1
fi
if [[ ${#updates[@]} -eq 0 ]]; then
    echo -e "${GREEN}Everything is up to date!${NC}"
    exit 0
fi
echo ""
echo 'Updates available:'
for component in "${updates[@]}"; do
    case "$component" in
        cpa) echo "  CPA              ${cpa_current} -> ${cpa_latest}" ;;
        cpamp) echo "  CPA Manager Plus ${cpamp_current} -> ${cpamp_latest}" ;;
    esac
done
if [[ "$AUTO_YES" != true ]]; then
    read -r -p 'Proceed? [y/N] ' confirm || confirm=n
    if [[ "$confirm" != y && "$confirm" != Y ]]; then
        echo 'Cancelled.'
        exit 0
    fi
fi
for component in "${updates[@]}"; do
    case "$component" in
        cpa)
            update_component CPA "$CPA_INSTALL_DIR" "$CPA_SERVICE_NAME" "$CPA_BINARY_NAME" "$cpa_asset" "$cpa_url" "$cpa_latest" 0 ;;
        cpamp)
            update_component 'CPA Manager Plus' "$cpamp_runtime" "$CPAMP_SERVICE_NAME" "$CPAMP_BINARY_NAME" "$cpamp_asset" "$cpamp_url" "$cpamp_latest" 0 "$cpamp_checksum_url" ;;
    esac
done
echo -e "\n${GREEN}All updates complete!${NC}"
