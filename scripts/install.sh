#!/bin/sh
# Plugin build step. Fetches the prebuilt binary for this platform so Go is
# not required; falls back to building from source when that isn't possible.
set -eu

REPO="DnzzL/herdr-automations"
OUT="bin/herdr-automations"
VERSION=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -1)

build_from_source() {
	if ! command -v go >/dev/null 2>&1; then
		echo "herdr-automations: no prebuilt binary for this platform and Go is not installed." >&2
		echo "Install Go (https://go.dev/dl/) and reinstall the plugin." >&2
		exit 1
	fi
	echo "herdr-automations: building from source"
	go build -ldflags "-X main.Version=${VERSION}" -o "$OUT" .
}

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) build_from_source; exit 0 ;;
esac

case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) build_from_source; exit 0 ;;
esac

asset="herdr-automations_${os}_${arch}"
base="https://github.com/${REPO}/releases/download/v${VERSION}"
mkdir -p bin
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if ! curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}"; then
	echo "herdr-automations: no release asset ${asset} for v${VERSION}"
	build_from_source
	exit 0
fi

# Verify against the release checksums when we can compute one locally.
if curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"; then
	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "${tmp}/${asset}" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "${tmp}/${asset}" | cut -d' ' -f1)
	else
		actual=""
	fi
	if [ -n "$actual" ]; then
		expected=$(grep " ${asset}\$" "${tmp}/checksums.txt" | cut -d' ' -f1)
		if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
			echo "herdr-automations: checksum mismatch for ${asset}, refusing the download." >&2
			build_from_source
			exit 0
		fi
	fi
fi

mv "${tmp}/${asset}" "$OUT"
chmod +x "$OUT"

# A binary that can't run here (wrong libc, bad download) must not ship.
if ! "./$OUT" version >/dev/null 2>&1; then
	echo "herdr-automations: prebuilt binary did not run, falling back"
	build_from_source
	exit 0
fi

echo "herdr-automations: installed prebuilt v${VERSION} (${os}/${arch})"
