#!/bin/bash
# CLIProxyAPI Updater
# Downloads the latest release from GitHub and restarts the service.
# Usage: bash update.sh

set -euo pipefail

REPO_OWNER="blue2517"
REPO_NAME="CLIProxyAPI"
INSTALL_DIR="${INSTALL_DIR:-$HOME/cliproxyapi}"
SERVICE_NAME="cliproxyapi"
BINARY_NAME="cli-proxy-api"
API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

detect_arch() {
    case "$(uname -m)" in
        x86_64)  echo "amd64" ;;
        aarch64) echo "aarch64" ;;
        *)       echo "$(uname -m)" ;;
    esac
}

get_current_version() {
    if [[ -f "${INSTALL_DIR}/version.txt" ]]; then
        tr -d '[:space:]' < "${INSTALL_DIR}/version.txt"
    else
        echo "unknown"
    fi
}

get_latest_release() {
    curl -fsSL "$API_URL"
}

echo -e "${BLUE}CLIProxyAPI Updater${NC}"
echo "================================"
echo ""

if [[ ! -d "$INSTALL_DIR" ]]; then
    echo -e "${RED}Install directory not found: ${INSTALL_DIR}${NC}"
    echo "Set INSTALL_DIR to your installation path."
    exit 1
fi

current_version=$(get_current_version)
echo -e "Current version: ${YELLOW}${current_version}${NC}"

echo "Checking for updates..."
release_json=$(get_latest_release) || {
    echo -e "${RED}Failed to fetch release info from GitHub.${NC}"
    exit 1
}

latest_tag=$(echo "$release_json" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
if [[ -z "$latest_tag" ]]; then
    echo -e "${RED}Failed to parse latest version.${NC}"
    exit 1
fi

latest_version="${latest_tag#v}"
echo -e "Latest version:  ${GREEN}${latest_version}${NC}"
echo ""

if [[ "$current_version" == "$latest_version" ]]; then
    echo -e "${GREEN}Already up to date!${NC}"
    exit 0
fi

read -rp "Update ${current_version} -> ${latest_version}? [y/N] " confirm
if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "Cancelled."
    exit 0
fi

arch=$(detect_arch)
asset_name="CLIProxyAPI_${latest_version}_linux_${arch}.tar.gz"
download_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${latest_tag}/${asset_name}"

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

echo ""
echo -e "${BLUE}[1/4]${NC} Downloading ${asset_name}..."
if ! curl -fsSL --retry 3 --retry-delay 2 -o "${tmp_dir}/${asset_name}" "$download_url"; then
    echo -e "${YELLOW}curl failed, falling back to wget...${NC}"
    if ! wget --tries=3 --timeout=30 -q -O "${tmp_dir}/${asset_name}" "$download_url"; then
        echo -e "${RED}Download failed.${NC}"
        echo "URL: ${download_url}"
        exit 1
    fi
fi

echo -e "${BLUE}[2/4]${NC} Extracting..."
tar -xzf "${tmp_dir}/${asset_name}" -C "$tmp_dir"
if [[ ! -f "${tmp_dir}/${BINARY_NAME}" ]]; then
    echo -e "${RED}Binary not found in archive.${NC}"
    exit 1
fi

echo -e "${BLUE}[3/4]${NC} Stopping service and replacing binary..."
systemctl stop "$SERVICE_NAME" 2>/dev/null || true

if [[ -f "${INSTALL_DIR}/${BINARY_NAME}" ]]; then
    cp "${INSTALL_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}.bak"
fi

mv "${tmp_dir}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
echo "$latest_version" > "${INSTALL_DIR}/version.txt"

echo -e "${BLUE}[4/4]${NC} Starting service..."
systemctl start "$SERVICE_NAME"

echo ""
echo -e "${GREEN}Updated to ${latest_version} successfully!${NC}"
echo -e "Backup saved as ${INSTALL_DIR}/${BINARY_NAME}.bak"
