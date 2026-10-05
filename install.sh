#!/bin/sh
set -eu

app=lazyp4
repo=https://github.com/Solessfir/$app

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

[ "$#" -le 1 ] || fail "Usage: $0 [install directory]"
[ "$(uname -s)" = Linux ] || fail "This installer supports Linux only."
case "$(uname -m)" in
    x86_64|amd64) arch=x86_64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "Supported architectures: x86_64 and ARM64." ;;
esac
for tool in curl tar sha256sum awk mktemp install mv; do
    command -v "$tool" >/dev/null 2>&1 || fail "Required command not found: $tool"
done

release=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repo/releases/latest")
tag=${release##*/}
case "$tag" in
    v[0-9]*) ;;
    *) fail "No stable release found at $repo/releases." ;;
esac
asset=${app}_linux_${arch}.tar.gz
base=$repo/releases/download/$tag
tmp=$(mktemp -d)
staged=''
trap 'rm -rf -- "$tmp"; if [ -n "$staged" ]; then rm -f -- "$staged"; fi' 0
trap 'exit 1' 1 2 15

curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
awk -v file="$asset" '$2 == file {print}' "$tmp/checksums.txt" > "$tmp/checksum.txt"
(cd "$tmp" && sha256sum --check --status checksum.txt) || fail "Checksum verification failed."
tar -xzf "$tmp/$asset" -C "$tmp" -- "$app"

dir=${1:-"$HOME/.local/bin"}
mkdir -p -- "$dir"
staged=$(mktemp -- "$dir/.$app.XXXXXX")
install -m 755 "$tmp/$app" "$staged"
mv -fT -- "$staged" "$dir/$app"
staged=''
printf 'Installed %s %s to %s\n' "$app" "$tag" "$dir/$app"
case ":${PATH:-}:" in
    *":$dir:"*) ;;
    *) printf 'Add %s to your PATH.\n' "$dir" ;;
esac
