#!/usr/bin/env sh
# shellcheck shell=dash
#
# Iceberg Sentry installer.
#
# Usage:
#   curl -sSL https://get.icebergsentry.io/install.sh | sh
#
# Environment overrides:
#   SENTRY_VERSION   — specific version (e.g. v0.3.0). Default: latest release.
#   SENTRY_PREFIX    — install dir. Default: $HOME/.local/bin.
#   SENTRY_REPO      — override GitHub owner/repo. Default: jaybilgaye/iceberg-sentry.
#   SENTRY_NO_VERIFY — set to 1 to skip checksum verification (not recommended).

set -eu

REPO="${SENTRY_REPO:-jaybilgaye/iceberg-sentry}"
PREFIX="${SENTRY_PREFIX:-$HOME/.local/bin}"

log()  { printf "%s\n" "$*" >&2; }
die()  { log "error: $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# ---- 1. Detect OS + arch --------------------------------------------------

detect_platform() {
    uname_s=$(uname -s)
    case "$uname_s" in
        Linux)  os=linux ;;
        Darwin) os=darwin ;;
        *) die "unsupported OS: $uname_s" ;;
    esac

    uname_m=$(uname -m)
    case "$uname_m" in
        x86_64|amd64) arch=amd64; arch_name=x86_64 ;;
        aarch64|arm64) arch=arm64; arch_name=arm64 ;;
        *) die "unsupported arch: $uname_m" ;;
    esac
    export os arch arch_name
}

# ---- 2. Resolve the version to install -----------------------------------

resolve_version() {
    if [ -n "${SENTRY_VERSION:-}" ]; then
        version="$SENTRY_VERSION"
        return
    fi
    have curl || die "curl is required"
    version=$(
        curl -fsSL -H "Accept: application/vnd.github+json" \
             "https://api.github.com/repos/$REPO/releases/latest" \
        | grep -m 1 '"tag_name"' \
        | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/'
    )
    [ -z "$version" ] && die "could not determine latest version — set SENTRY_VERSION"
}

# ---- 3. Download the archive + checksums ---------------------------------

download() {
    # v-prefix stripped for the archive naming (goreleaser convention)
    ver_no_v=${version#v}
    archive_name="iceberg-sentry_${ver_no_v}_${os}_${arch_name}.tar.gz"
    if [ "$os" = "windows" ]; then
        archive_name="iceberg-sentry_${ver_no_v}_${os}_${arch_name}.zip"
    fi
    checksums_name="iceberg-sentry_${ver_no_v}_checksums.txt"

    base="https://github.com/$REPO/releases/download/${version}"
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT

    log "Downloading ${archive_name}..."
    curl -fL -o "$tmp/$archive_name"    "$base/$archive_name"    || die "download failed"

    if [ -z "${SENTRY_NO_VERIFY:-}" ]; then
        log "Downloading checksums..."
        curl -fL -o "$tmp/$checksums_name" "$base/$checksums_name" || die "checksum download failed"

        expected=$(grep " $archive_name\$" "$tmp/$checksums_name" | awk '{print $1}')
        [ -z "$expected" ] && die "checksum entry for $archive_name not found"

        if have sha256sum;   then actual=$(sha256sum   "$tmp/$archive_name" | awk '{print $1}');
        elif have shasum;    then actual=$(shasum -a 256 "$tmp/$archive_name" | awk '{print $1}');
        else die "no sha256 tool available (need sha256sum or shasum)"
        fi
        if [ "$expected" != "$actual" ]; then
            die "checksum mismatch: expected $expected, got $actual"
        fi
        log "Checksum verified."
    else
        log "SENTRY_NO_VERIFY set — skipping checksum."
    fi

    log "Extracting..."
    tar -xzf "$tmp/$archive_name" -C "$tmp"

    export bin_path="$tmp/iceberg-sentry"
    [ -f "$bin_path" ] || die "extracted archive missing iceberg-sentry binary"
}

# ---- 4. Install -----------------------------------------------------------

install() {
    mkdir -p "$PREFIX"
    target="$PREFIX/iceberg-sentry"
    if [ -w "$PREFIX" ]; then
        install -m 0755 "$bin_path" "$target"
    else
        log "Install prefix $PREFIX is not writable — using sudo."
        sudo install -m 0755 "$bin_path" "$target"
    fi
    log ""
    log "Installed iceberg-sentry ${version} → $target"
    log ""

    case ":$PATH:" in
        *":$PREFIX:"*) ;;
        *)
            log "note: $PREFIX is not on your PATH. Add:"
            log "  export PATH=\"\$PATH:$PREFIX\""
            log ""
            ;;
    esac

    "$target" version 2>/dev/null || true
}

main() {
    detect_platform
    resolve_version
    download
    install
}

main "$@"
