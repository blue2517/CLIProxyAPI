#!/bin/bash
# CPA & Keeper Updater
# Checks for updates and restarts services as needed.
# Usage: bash update.sh [--yes]

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

AUTO_YES=false
if [[ "${1:-}" == "--yes" || "${1:-}" == "-y" ]]; then
    AUTO_YES=true
fi

# ── CPA config ──────────────────────────────────────────────────
CPA_REPO_OWNER="blue2517"
CPA_REPO_NAME="CLIProxyAPI"
CPA_INSTALL_DIR="${CPA_INSTALL_DIR:-$HOME/cliproxyapi}"
CPA_SERVICE_NAME="cliproxyapi"
CPA_BINARY_NAME="cli-proxy-api"
CPA_API_URL="https://api.github.com/repos/${CPA_REPO_OWNER}/${CPA_REPO_NAME}/releases/latest"

# ── Keeper config ───────────────────────────────────────────────
KEEPER_REPO_OWNER="blue2517"
KEEPER_REPO_NAME="cpa-usage-keeper"
KEEPER_INSTALL_DIR="${KEEPER_INSTALL_DIR:-$HOME/cpa-usage-keeper}"
KEEPER_SERVICE_NAME="cpa-usage-keeper"
KEEPER_BINARY_NAME="cpa-usage-keeper"
KEEPER_API_URL="https://api.github.com/repos/${KEEPER_REPO_OWNER}/${KEEPER_REPO_NAME}/releases/latest"

detect_arch() {
    case "$(uname -m)" in
        x86_64)  echo "amd64" ;;
        aarch64) echo "arm64" ;;
        *)       echo "$(uname -m)" ;;
    esac
}

get_current_version() {
    local install_dir="$1"
    if [[ -f "${install_dir}/version.txt" ]]; then
        tr -d '[:space:]' < "${install_dir}/version.txt"
    else
        echo "unknown"
    fi
}

get_latest_release() {
    local api_url="$1"
    curl -fsSL "$api_url"
}

parse_tag() {
    echo "$1" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/'
}

download() {
    local url="$1" dest="$2"
    if ! curl -fsSL --retry 3 --retry-delay 2 -o "$dest" "$url"; then
        echo -e "${YELLOW}curl failed, falling back to wget...${NC}"
        if ! wget --tries=3 --timeout=30 -q -O "$dest" "$url"; then
            echo -e "${RED}Download failed: ${url}${NC}"
            return 1
        fi
    fi
}

update_component() {
    local name="$1" install_dir="$2" service_name="$3" binary_name="$4" asset_name="$5" download_url="$6" latest_version="$7" strip="${8:-0}"

    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' RETURN

    echo ""
    echo -e "${BLUE}[${name} 1/4]${NC} Downloading ${asset_name}..."
    download "$download_url" "${tmp_dir}/${asset_name}" || return 1

    echo -e "${BLUE}[${name} 2/4]${NC} Extracting..."
    tar -xzf "${tmp_dir}/${asset_name}" -C "$tmp_dir" --strip-components="$strip"
    if [[ ! -f "${tmp_dir}/${binary_name}" ]]; then
        echo -e "${RED}Binary not found in archive.${NC}"
        return 1
    fi

    echo -e "${BLUE}[${name} 3/4]${NC} Stopping service and replacing binary..."
    systemctl stop "$service_name" 2>/dev/null || true

    if [[ -f "${install_dir}/${binary_name}" ]]; then
        cp "${install_dir}/${binary_name}" "${install_dir}/${binary_name}.bak"
    fi

    mv "${tmp_dir}/${binary_name}" "${install_dir}/${binary_name}"
    chmod +x "${install_dir}/${binary_name}"
    echo "$latest_version" > "${install_dir}/version.txt"

    echo -e "${BLUE}[${name} 4/4]${NC} Starting service..."
    systemctl start "$service_name"

    echo -e "${GREEN}${name} updated to ${latest_version}!${NC}"
}

# ── Main ────────────────────────────────────────────────────────

echo -e "${BLUE}CPA & Keeper Updater${NC}"
echo "================================"
echo ""

arch=$(detect_arch)
updates=()

# ── Check CPA ───────────────────────────────────────────────────

if [[ -d "$CPA_INSTALL_DIR" ]]; then
    cpa_current=$(get_current_version "$CPA_INSTALL_DIR")
    echo -e "CPA    current: ${YELLOW}${cpa_current}${NC}"

    cpa_release=$(get_latest_release "$CPA_API_URL" 2>/dev/null) || true
    if [[ -n "${cpa_release:-}" ]]; then
        cpa_tag=$(parse_tag "$cpa_release")
        cpa_latest="${cpa_tag#v}"
        echo -e "CPA    latest:  ${GREEN}${cpa_latest}${NC}"
        if [[ "$cpa_current" != "$cpa_latest" ]]; then
            updates+=("cpa")
        else
            echo -e "CPA    ${GREEN}up to date${NC}"
        fi
    else
        echo -e "CPA    ${RED}failed to fetch release info${NC}"
    fi
else
    echo -e "CPA    ${YELLOW}not installed (${CPA_INSTALL_DIR})${NC}"
fi

echo ""

# ── Check Keeper ────────────────────────────────────────────────

if [[ -d "$KEEPER_INSTALL_DIR" ]]; then
    keeper_current=$(get_current_version "$KEEPER_INSTALL_DIR")
    echo -e "Keeper current: ${YELLOW}${keeper_current}${NC}"

    keeper_release=$(get_latest_release "$KEEPER_API_URL" 2>/dev/null) || true
    if [[ -n "${keeper_release:-}" ]]; then
        keeper_tag=$(parse_tag "$keeper_release")
        keeper_latest="${keeper_tag#v}"
        echo -e "Keeper latest:  ${GREEN}${keeper_latest}${NC}"
        if [[ "$keeper_current" != "$keeper_latest" ]]; then
            updates+=("keeper")
        else
            echo -e "Keeper ${GREEN}up to date${NC}"
        fi
    else
        echo -e "Keeper ${RED}failed to fetch release info${NC}"
    fi
else
    echo -e "Keeper ${YELLOW}not installed (${KEEPER_INSTALL_DIR})${NC}"
fi

echo ""

# ── Apply updates ───────────────────────────────────────────────

if [[ ${#updates[@]} -eq 0 ]]; then
    echo -e "${GREEN}Everything is up to date!${NC}"
    exit 0
fi

summary=""
for u in "${updates[@]}"; do
    case "$u" in
        cpa)    summary="${summary}  CPA    ${cpa_current} -> ${cpa_latest}\n" ;;
        keeper) summary="${summary}  Keeper ${keeper_current} -> ${keeper_latest}\n" ;;
    esac
done

echo -e "Updates available:\n${summary}"

if [[ "$AUTO_YES" != true ]]; then
    read -rp "Proceed? [y/N] " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
        echo "Cancelled."
        exit 0
    fi
fi

for u in "${updates[@]}"; do
    case "$u" in
        cpa)
            cpa_asset="CLIProxyAPI_${cpa_latest}_linux_${arch}.tar.gz"
            cpa_url="https://github.com/${CPA_REPO_OWNER}/${CPA_REPO_NAME}/releases/download/${cpa_tag}/${cpa_asset}"
            update_component "CPA" "$CPA_INSTALL_DIR" "$CPA_SERVICE_NAME" "$CPA_BINARY_NAME" "$cpa_asset" "$cpa_url" "$cpa_latest" 0
            ;;
        keeper)
            keeper_asset="cpa-usage-keeper_${keeper_tag}_linux_${arch}.tar.gz"
            keeper_url="https://github.com/${KEEPER_REPO_OWNER}/${KEEPER_REPO_NAME}/releases/download/${keeper_tag}/${keeper_asset}"
            update_component "Keeper" "$KEEPER_INSTALL_DIR" "$KEEPER_SERVICE_NAME" "$KEEPER_BINARY_NAME" "$keeper_asset" "$keeper_url" "$keeper_latest" 1
            ;;
    esac
done

echo ""
echo -e "${GREEN}All updates complete!${NC}"
