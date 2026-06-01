#!/bin/sh
# SkillFleet installer (POSIX sh). Downloads a prebuilt agent or server
# binary from GitHub Releases for the current platform, verifies its
# SHA256, and installs it to a bin directory on PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | sh
#
# Environment overrides:
#   SKILLFLEET_COMPONENT  agent (default) | server
#   SKILLFLEET_VERSION    a release tag (default: latest)
#   INSTALL_DIR           target dir (default: /usr/local/bin if writable,
#                         else ~/.local/bin)
#
# SQLite is pure Go, so the binaries are static and dependency-free.
set -eu

REPO="yeluonight/skillfleet"
COMPONENT="${SKILLFLEET_COMPONENT:-agent}"
VERSION="${SKILLFLEET_VERSION:-latest}"

case "$COMPONENT" in
  agent | server) ;;
  *)
    echo "error: SKILLFLEET_COMPONENT must be 'agent' or 'server', got '$COMPONENT'" >&2
    exit 1
    ;;
esac

# --- detect OS / ARCH and map to the release asset naming ---
os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *)
    echo "error: unsupported OS '$os' (this installer covers linux/darwin; on Windows use install.ps1)" >&2
    exit 1
    ;;
esac
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  aarch64 | arm64) arch="arm64" ;;
  *)
    echo "error: unsupported architecture '$arch'" >&2
    exit 1
    ;;
esac

asset="skillfleet-${COMPONENT}-${os}-${arch}"

# --- resolve the download base URL ---
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

# --- pick an install dir on PATH ---
if [ -n "${INSTALL_DIR:-}" ]; then
  install_dir="$INSTALL_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
  install_dir="/usr/local/bin"
else
  install_dir="${HOME}/.local/bin"
fi
mkdir -p "$install_dir"

# --- need a downloader ---
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
else
  echo "error: need curl or wget to download" >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading ${asset} (${VERSION})"
fetch "${base}/${asset}" "${tmp}/${asset}"

# --- verify SHA256 against the published SHA256SUMS ---
if fetch "${base}/SHA256SUMS" "${tmp}/SHA256SUMS" 2>/dev/null; then
  want=$(grep " ${asset}\$" "${tmp}/SHA256SUMS" | awk '{print $1}')
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "${tmp}/${asset}" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')
    else
      got=""
      echo "warning: no sha256 tool found; skipping checksum verification" >&2
    fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      echo "error: checksum mismatch for ${asset}" >&2
      echo "  want $want" >&2
      echo "  got  $got" >&2
      exit 1
    fi
    [ -n "$got" ] && echo "==> checksum ok"
  fi
else
  echo "warning: could not fetch SHA256SUMS; skipping checksum verification" >&2
fi

# --- install ---
dest="${install_dir}/skillfleet-${COMPONENT}"
chmod +x "${tmp}/${asset}"
mv "${tmp}/${asset}" "$dest"
echo "==> installed ${dest}"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) echo "note: ${install_dir} is not on your PATH; add it to use 'skillfleet-${COMPONENT}' directly" >&2 ;;
esac

# --- next-step hints ---
if [ "$COMPONENT" = "agent" ]; then
  cat >&2 <<'EOF'

Next steps:
  1. skillfleet-agent enroll <server-url> <token>     # token from the WebUI
  2. Approve the device in the WebUI (Devices page).
  3. skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills
  4. skillfleet-agent                                  # run the agent loop
EOF
else
  cat >&2 <<'EOF'

Next steps:
  skillfleet-server                                    # starts on :7890; prints a setup code
  Open http://<host>:7890 and complete setup with that code.
EOF
fi
