#!/usr/bin/env sh
# Astrolift CLI authenticated release installer (#17).
#
# Usage from an authenticated source checkout:
#   gh auth login
#   ./scripts/install.sh
#
# Detects OS + architecture, verifies the latest GitHub release archive,
# and installs the binary in ASTRO_INSTALL_DIR, /usr/local/bin, or ~/.local/bin.
# Set ASTRO_INSTALL_TAG to pin a release. ASTRO_INSTALL_BASE_URL remains
# available for an authenticated/public mirror or offline test fixture.

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
ASTRO_INSTALL_TMP="$(mktemp -d)"
trap 'rm -rf "${ASTRO_INSTALL_TMP}"' EXIT

if [ -n "${ASTRO_INSTALL_BASE_URL:-}" ]; then
    URL="${ASTRO_INSTALL_BASE_URL}/${ARCHIVE}"
    CHECKSUM_URL="${ASTRO_INSTALL_BASE_URL}/${BINARY}-checksums.txt"
    echo "Fetching ${URL}..."
    curl -fSL --retry 3 --retry-delay 2 "${URL}" -o "${ASTRO_INSTALL_TMP}/${ARCHIVE}" || err "download failed"
    curl -fSL --retry 3 --retry-delay 2 "${CHECKSUM_URL}" -o "${ASTRO_INSTALL_TMP}/${BINARY}-checksums.txt" || err "checksum download failed"
else
    command -v gh >/dev/null 2>&1 || err "GitHub CLI is required to download private Astrolift releases"
    gh auth status --hostname github.com >/dev/null 2>&1 || err "run 'gh auth login' with an account that can read ${REPO}"
    TAG="${ASTRO_INSTALL_TAG:-}"
    if [ -z "${TAG}" ]; then
        TAG="$(gh release view --repo "${REPO}" --json tagName --jq .tagName)" || err "could not resolve the latest release"
    fi
    echo "Fetching ${ARCHIVE} from ${REPO} ${TAG}..."
    gh release download "${TAG}" \
        --repo "${REPO}" \
        --pattern "${ARCHIVE}" \
        --pattern "${BINARY}-checksums.txt" \
        --dir "${ASTRO_INSTALL_TMP}" || err "authenticated release download failed"
fi

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
if [ -f "share/man/man1/${BINARY}.1" ]; then
    MAN_DIR="${ASTRO_MAN_DIR:-$(dirname "${INSTALL_DIR}")/share/man/man1}"
    mkdir -p "${MAN_DIR}"
    install -m 0644 "share/man/man1/${BINARY}.1" "${MAN_DIR}/${BINARY}.1"
    echo "Installed man page to ${MAN_DIR}/${BINARY}.1"
fi
if [ "${INSTALL_DIR}" = "${USER_BIN}" ]; then
    case ":${PATH}:" in
        *":${USER_BIN}:"*) ;;
        *) echo "warning: ${USER_BIN} is not in your PATH; add it to your shell config" ;;
    esac
fi

"${INSTALL_DIR}/${BINARY}" version
