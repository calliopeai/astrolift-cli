#!/usr/bin/env sh
# Astrolift CLI curl|sh installer (#17).
#
# Usage: curl -fsSL https://astrolift.app/cli/install.sh | sh
#
# Detects OS + architecture, fetches the latest GitHub release
# tarball/zip, extracts the binary, and drops it in either
# /usr/local/bin (when writable) or ~/.local/bin.

set -e

REPO="calliopeai/astrolift-cli"
BINARY="astro"
INSTALL_DIR_DEFAULT="/usr/local/bin"
USER_BIN="${HOME}/.local/bin"

err() { printf '%s\n' "error: $*" >&2; exit 1; }

# Detect OS
case "$(uname -s)" in
    Linux*)  OS=Linux ;;
    Darwin*) OS=Darwin ;;
    *)       err "unsupported OS: $(uname -s) (use the binary download from GitHub releases)" ;;
esac

# Detect architecture
case "$(uname -m)" in
    x86_64|amd64) ARCH=x86_64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *)            err "unsupported architecture: $(uname -m)" ;;
esac

# Pick install dir — prefer system if writable, else user-local
if [ -w "${INSTALL_DIR_DEFAULT}" ]; then
    INSTALL_DIR="${INSTALL_DIR_DEFAULT}"
else
    INSTALL_DIR="${USER_BIN}"
    mkdir -p "${INSTALL_DIR}"
fi

# Resolve latest version (GitHub redirects /releases/latest to the latest tag)
VERSION="$(curl -fsSL -o /dev/null -w '%{redirect_url}' \
    "https://github.com/${REPO}/releases/latest" \
    | sed 's|.*/tag/v\?||')"
[ -n "${VERSION}" ] || err "couldn't resolve latest CLI version"

ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ARCHIVE}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Fetching ${URL}..."
curl -fsSL "${URL}" -o "${TMPDIR}/${ARCHIVE}" || err "download failed"

cd "${TMPDIR}"
tar -xzf "${ARCHIVE}"

mv "${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo "Installed astro ${VERSION} to ${INSTALL_DIR}/${BINARY}"
if [ "${INSTALL_DIR}" = "${USER_BIN}" ]; then
    case ":${PATH}:" in
        *":${USER_BIN}:"*) ;;
        *) echo "warning: ${USER_BIN} is not in your PATH; add it to your shell config" ;;
    esac
fi

"${INSTALL_DIR}/${BINARY}" version
