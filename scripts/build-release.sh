#!/bin/sh
# Builds morse for every platform a release ships, and packages each build with
# the files a user needs next to the binary. CI runs this on a tag; running it
# by hand produces the same files, which is how to check a change to it.
#
#   scripts/build-release.sh v0.6.0 dist
#
# Writes one archive per platform and a SHA256SUMS file into the directory.

set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: build-release.sh VERSION OUT_DIR" >&2
    exit 2
fi
version=$1
out=$2

# Every target is a static, cgo-free binary: morse uses nothing but the
# standard library and yaml, so one build runs on any machine of that platform,
# with no libc to match. linux/arm is ARMv7, the 32-bit Raspberry Pi OS.
targets="
linux/amd64
linux/arm64
linux/arm
darwin/amd64
darwin/arm64
windows/amd64
windows/arm64
freebsd/amd64
"

go=${GO:-go}
root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for target in $targets; do
    os=${target%/*}
    arch=${target#*/}
    label=$arch
    [ "$arch" = arm ] && label=armv7
    # No version in the name: a release's files then have the same names every
    # time, so releases/latest/download/<name> is a link that never goes stale.
    # The binary itself says which release it is, with `morse version`.
    name="morse_${os}_${label}"
    stage="$work/$name"
    mkdir -p "$stage"

    exe=morse
    [ "$os" = windows ] && exe=morse.exe

    # -trimpath keeps the build machine's paths out of the binary; the version
    # is stamped the same way the Makefile and the Homebrew formula do it.
    (cd "$root" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=7 \
        "$go" build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/$exe" .)
    cp "$root/LICENSE" "$root/README.md" "$root/config.yaml.example" "$stage/"

    # zip for Windows, where it is what Explorer opens; tar.gz everywhere else,
    # where it keeps the executable bit.
    if [ "$os" = windows ]; then
        (cd "$work" && zip -qr "$out/$name.zip" "$name")
    else
        tar -C "$work" -czf "$out/$name.tar.gz" "$name"
    fi
    echo "built $name"
done

(cd "$out" && sha256sum morse_* > SHA256SUMS)
echo "wrote $out/SHA256SUMS"
