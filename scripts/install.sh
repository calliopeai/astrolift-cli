#!/usr/bin/env sh
# Astrolift CLI public release installer (#17, #53, #134).
#
# Downloads checksum-verified archives from the public release repository.
# An ASTRO_INSTALL_BASE_URL mirror takes priority, followed by an authenticated
# GitHub CLI, an explicit token, or anonymous GitHub API requests.
#
# Usage: ./scripts/install.sh
# Set ASTRO_INSTALL_TAG to pin a release. Installs into ASTRO_INSTALL_DIR,
# /usr/local/bin, or ~/.local/bin, and installs the archive's man page.

set -e

REPO="calliopeai/astrolift-cli"
BINARY="astro"
INSTALL_DIR_DEFAULT="/usr/local/bin"
USER_BIN="${HOME}/.local/bin"
# Overridable for tests and GitHub Enterprise mirrors.
API_URL="${ASTRO_GITHUB_API_URL:-https://api.github.com}"
API_URL="${API_URL%/}"

err() { printf '%s\n' "error: $*" >&2; exit 1; }

# Explain access failures without assuming every missing release is an auth error.
err_release_access() {
    _status="$1"
    _url="$2"
    case "${_status}" in
        401) err "GitHub returned 401: rejected authentication for ${_url}; refresh or remove the configured token" ;;
        403) err "GitHub returned 403: denied ${_url}; check access or rate limits and retry later (an authenticated gh or token raises the API limit)" ;;
        404) err "GitHub returned 404: release or asset not found at ${_url}; check ASTRO_INSTALL_TAG and repository access (a wrong or unscoped token can also hide a release)" ;;
    esac
}

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

# Must match `archives.name_template` in .goreleaser.yaml:
# astro-linux-amd64.tar.gz, astro-darwin-arm64.tar.gz, ... Hyphens, no version
# segment, Go's own os/arch spelling (#53).
ARCHIVE="${BINARY}-${OS}-${ARCH}.tar.gz"
CHECKSUMS="${BINARY}-checksums.txt"
ASTRO_INSTALL_TMP="$(mktemp -d)"
trap 'rm -rf "${ASTRO_INSTALL_TMP}"' EXIT

TOKEN="${ASTRO_GITHUB_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"

# api_get fetches an API document, mapping auth/visibility failures to advice.
api_get() {
    _url="$1"
    _out="$2"
    set --
    if [ -n "${TOKEN}" ]; then
        set -- -H "Authorization: Bearer ${TOKEN}"
    fi
    _code="$(curl -sS "$@" -o "${_out}" -w '%{http_code}' \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "${_url}" || echo 000)"
    case "${_code}" in
        200) ;;
        401|403|404) err_release_access "${_code}" "${_url}" ;;
        000) err "could not reach ${_url}" ;;
        *)   err "GitHub API returned ${_code} for ${_url}" ;;
    esac
}

# release_asset_id extracts an asset's numeric id from a release document.
# Splitting on '{' puts each asset object on its own line, so the "id" and
# "name" of one asset stay together and can be matched as a pair. Avoids a
# jq dependency, which is absent from most minimal container images.
release_asset_id() {
    tr -d '\n' < "$1" \
        | tr '{' '\n' \
        | grep "\"name\"[[:space:]]*:[[:space:]]*\"$2\"" \
        | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' \
        | head -n 1
}

# The API asset endpoint supports anonymous public downloads and authenticated
# private mirrors. curl does not forward credentials to another redirect host.
api_download_asset() {
    _id="$1"
    _dest="$2"
    _url="${API_URL}/repos/${REPO}/releases/assets/${_id}"
    echo "Fetching ${_url}"
    set --
    if [ -n "${TOKEN}" ]; then
        set -- -H "Authorization: Bearer ${TOKEN}"
    fi
    _code="$(curl -sSL "$@" --retry 3 --retry-delay 2 -o "${_dest}" -w '%{http_code}' \
        -H "Accept: application/octet-stream" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "${_url}" || echo 000)"
    case "${_code}" in
        200) ;;
        401|403|404) err_release_access "${_code}" "${_url}" ;;
        *)   err "asset download returned ${_code} for ${_url}" ;;
    esac
}

if [ -n "${ASTRO_INSTALL_BASE_URL:-}" ]; then
    URL="${ASTRO_INSTALL_BASE_URL}/${ARCHIVE}"
    CHECKSUM_URL="${ASTRO_INSTALL_BASE_URL}/${CHECKSUMS}"
    echo "Fetching ${URL}..."
    curl -fSL --retry 3 --retry-delay 2 "${URL}" -o "${ASTRO_INSTALL_TMP}/${ARCHIVE}" || err "download failed"
    curl -fSL --retry 3 --retry-delay 2 "${CHECKSUM_URL}" -o "${ASTRO_INSTALL_TMP}/${CHECKSUMS}" || err "checksum download failed"
elif command -v gh >/dev/null 2>&1 && gh auth status --hostname github.com >/dev/null 2>&1; then
    TAG="${ASTRO_INSTALL_TAG:-}"
    if [ -z "${TAG}" ]; then
        TAG="$(gh release view --repo "${REPO}" --json tagName --jq .tagName)" || err "could not resolve the latest release"
    fi
    echo "Fetching ${ARCHIVE} from ${REPO} ${TAG}..."
    gh release download "${TAG}" \
        --repo "${REPO}" \
        --pattern "${ARCHIVE}" \
        --pattern "${CHECKSUMS}" \
        --dir "${ASTRO_INSTALL_TMP}" || err "authenticated release download failed"
else
    # No usable GitHub CLI: use the release API, with a token only if configured.
    # Public releases work anonymously in containers and fresh installations.
    if [ -n "${ASTRO_INSTALL_TAG:-}" ]; then
        RELEASE_URL="${API_URL}/repos/${REPO}/releases/tags/${ASTRO_INSTALL_TAG}"
    else
        RELEASE_URL="${API_URL}/repos/${REPO}/releases/latest"
    fi
    echo "Resolving ${RELEASE_URL}"
    api_get "${RELEASE_URL}" "${ASTRO_INSTALL_TMP}/release.json"

    ARCHIVE_ID="$(release_asset_id "${ASTRO_INSTALL_TMP}/release.json" "${ARCHIVE}")"
    [ -n "${ARCHIVE_ID}" ] || err "release has no asset named ${ARCHIVE} (expected the GoReleaser archive name)"
    CHECKSUM_ID="$(release_asset_id "${ASTRO_INSTALL_TMP}/release.json" "${CHECKSUMS}")"
    [ -n "${CHECKSUM_ID}" ] || err "release has no asset named ${CHECKSUMS}"

    api_download_asset "${ARCHIVE_ID}" "${ASTRO_INSTALL_TMP}/${ARCHIVE}"
    api_download_asset "${CHECKSUM_ID}" "${ASTRO_INSTALL_TMP}/${CHECKSUMS}"
fi

EXPECTED="$(awk -v archive="${ARCHIVE}" '$2 == archive { print $1; exit }' "${ASTRO_INSTALL_TMP}/${CHECKSUMS}")"
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
