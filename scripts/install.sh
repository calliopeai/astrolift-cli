#!/usr/bin/env sh
# Astrolift CLI curl|sh installer (#17).
#
# Usage: curl -fsSL https://raw.githubusercontent.com/calliopeai/astrolift-cli/main/scripts/install.sh | sh
#
# Detects OS + architecture, verifies the latest GitHub release archive,
# and installs the binary in ASTRO_INSTALL_DIR, /usr/local/bin, or ~/.local/bin.

set -e

REPO="calliopeai/astrolift-cli"
BINARY="astro"
INSTALL_DIR_DEFAULT="/usr/local/bin"
USER_BIN="${HOME}/.local/bin"

err() { printf '%s\n' "error: $*" >&2; exit 1; }

# Detect OS (GoReleaser asset names are lower-case).
case "$(uname -s)" in
    Linux*)  OS=linux ;;
    Darwin*) OS=darwin ;;
    *)       err "unsupported OS: $(uname -s) (use the binary download from GitHub releases)" ;;
esac

# Detect architecture
case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *)            err "unsupported architecture: $(uname -m)" ;;
esac

# Pick install dir — explicit override, then writable system dir, then user-local.
if [ -n "${ASTRO_INSTALL_DIR:-}" ]; then
    INSTALL_DIR="${ASTRO_INSTALL_DIR}"
elif [ -w "${INSTALL_DIR_DEFAULT}" ]; then
    INSTALL_DIR="${INSTALL_DIR_DEFAULT}"
else
    INSTALL_DIR="${USER_BIN}"
fi
mkdir -p "${INSTALL_DIR}"

ARCHIVE="${BINARY}-${OS}-${ARCH}.tar.gz"
BASE_URL="${ASTRO_INSTALL_BASE_URL:-https://github.com/${REPO}/releases/latest/download}"
URL="${BASE_URL}/${ARCHIVE}"
CHECKSUM_URL="${BASE_URL}/${BINARY}-checksums.txt"

ASTRO_INSTALL_TMP="$(mktemp -d)"
trap 'rm -rf "${ASTRO_INSTALL_TMP}"' EXIT

echo "Fetching ${URL}..."
curl -fSL --retry 3 --retry-delay 2 "${URL}" -o "${ASTRO_INSTALL_TMP}/${ARCHIVE}" || err "download failed"
curl -fSL --retry 3 --retry-delay 2 "${CHECKSUM_URL}" -o "${ASTRO_INSTALL_TMP}/${BINARY}-checksums.txt" || err "checksum download failed"

EXPECTED="$(awk -v archive="${ARCHIVE}" '$2 == archive { print $1; exit }' "${ASTRO_INSTALL_TMP}/${BINARY}-checksums.txt")"
[ -n "${EXPECTED}" ] || err "release checksum does not list ${ARCHIVE}"
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "${ASTRO_INSTALL_TMP}/${ARCHIVE}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "${ASTRO_INSTALL_TMP}/${ARCHIVE}" | awk '{print $1}')"
else
    err "sha256sum or shasum is required to verify the release"
fi
[ "${ACTUAL}" = "${EXPECTED}" ] || err "checksum mismatch for ${ARCHIVE}"

cd "${ASTRO_INSTALL_TMP}"
tar -xzf "${ARCHIVE}"
[ -f "${BINARY}" ] || err "archive does not contain ${BINARY}"

install -m 0755 "${BINARY}" "${INSTALL_DIR}/${BINARY}"

echo "Installed to ${INSTALL_DIR}/${BINARY}"
if [ "${INSTALL_DIR}" = "${USER_BIN}" ]; then
    case ":${PATH}:" in
        *":${USER_BIN}:"*) ;;
        *) echo "warning: ${USER_BIN} is not in your PATH; add it to your shell config" ;;
    esac
fi

"${INSTALL_DIR}/${BINARY}" version
