#!/bin/sh
# Installs morse from a GitHub release:
#
#   curl -fsSL https://raw.githubusercontent.com/leue21/morse/main/install.sh | sh
#
# It picks the archive for this machine, checks it against the release's
# SHA256SUMS, and puts the binary in ~/.local/bin. Nothing needs root, and the
# config is left alone. Settings, all optional:
#
#   MORSE_VERSION=v0.6.0       a release tag instead of the latest (v0.6.0 is
#                              the first with binaries)
#   MORSE_INSTALL_DIR=/path    where the binary goes (default ~/.local/bin)
#   MORSE_BASE_URL=url         where the archives are, for a mirror or a test;
#                              overrides MORSE_VERSION
#
# Pass them to sh, not to curl:  curl ... | MORSE_VERSION=v0.6.0 sh

set -eu

repo=leue21/morse

say() { printf 'morse install: %s\n' "$*"; }
die() { printf 'morse install: %s\n' "$*" >&2; exit 1; }

# platform prints the archive name for this machine, as build-release.sh names
# them, without the extension.
platform() {
    case $(uname -s) in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        FreeBSD) os=freebsd ;;
        MINGW* | MSYS* | CYGWIN*)
            die "on Windows, download morse_windows_amd64.zip from https://github.com/$repo/releases/latest" ;;
        *) die "no release for $(uname -s); build from source: go install github.com/$repo@latest" ;;
    esac
    case $(uname -m) in
        x86_64 | amd64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        armv7* | armv8l) arch=armv7 ;;
        *) die "no release for $os on $(uname -m); build from source: go install github.com/$repo@latest" ;;
    esac
    # A shell under Rosetta reports x86_64 on Apple silicon; the native build
    # is the one to install.
    if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
        [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = 1 ]; then
        arch=arm64
    fi
    # A 32-bit userland on a 64-bit kernel (Raspberry Pi OS armhf on a Pi 4 or
    # 5) cannot run the arm64 build, and uname -m says aarch64 there.
    if [ "$os" = linux ] && [ "$arch" = arm64 ] && command -v getconf >/dev/null 2>&1 &&
        [ "$(getconf LONG_BIT)" = 32 ]; then
        arch=armv7
    fi
    if [ "$os" = freebsd ] && [ "$arch" != amd64 ]; then
        die "no release for freebsd on $arch; build from source: go install github.com/$repo@latest"
    fi
    echo "morse_${os}_${arch}"
}

fetch() { # url file
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 2 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        die "needs curl or wget to download"
    fi || die "could not download $1${hint:-}"
}

sha256() { # file
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v sha256 >/dev/null 2>&1; then
        sha256 -q "$1"
    else
        die "needs sha256sum, shasum or sha256 to check the download"
    fi
}

main() {
    name=$(platform)
    if [ -n "${MORSE_BASE_URL:-}" ]; then
        base=${MORSE_BASE_URL%/}
    elif [ -n "${MORSE_VERSION:-}" ]; then
        base="https://github.com/$repo/releases/download/$MORSE_VERSION"
        hint=" — is $MORSE_VERSION a release? Binaries start at v0.6.0"
    else
        base="https://github.com/$repo/releases/latest/download"
    fi
    dir=${MORSE_INSTALL_DIR:-$HOME/.local/bin}

    work=$(mktemp -d)
    trap 'rm -rf "$work"' EXIT

    say "downloading $name from $base"
    fetch "$base/$name.tar.gz" "$work/$name.tar.gz"
    fetch "$base/SHA256SUMS" "$work/SHA256SUMS"

    # The archive is checked before anything in it is unpacked or run.
    want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$work/SHA256SUMS")
    [ -n "$want" ] || die "$name.tar.gz is not listed in SHA256SUMS"
    got=$(sha256 "$work/$name.tar.gz")
    [ "$got" = "$want" ] || die "checksum mismatch for $name.tar.gz: got $got, want $want"

    tar -xzf "$work/$name.tar.gz" -C "$work"
    mkdir -p "$dir"
    # Copied beside the target and renamed over it, so a morse that is running
    # meanwhile, or a copy cut short, never leaves a broken binary in place.
    cp "$work/$name/morse" "$dir/.morse.new"
    chmod 755 "$dir/.morse.new"
    mv -f "$dir/.morse.new" "$dir/morse"
    say "installed $("$dir/morse" version) to $dir/morse"

    case ":$PATH:" in
        *":$dir:"*) ;;
        *) say "$dir is not on your PATH; add it, e.g. in ~/.profile:"
           # shellcheck disable=SC2016 # $PATH is meant literally here
           printf '\n    export PATH="%s:$PATH"\n\n' "$dir" ;;
    esac

    config=$HOME/.config/morse/config.yaml
    if [ ! -f "$config" ]; then
        say "next: set MORSE_BOT_TOKEN and MORSE_CHAT_ID, or write $config"
        say "  (example: https://github.com/$repo/blob/main/config.yaml.example)"
        say "then check it with: morse capabilities"
    fi
}

# The only line that does anything is the last, so a download cut off halfway
# runs nothing rather than the first half of the script.
main "$@"
