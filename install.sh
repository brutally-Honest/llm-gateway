#!/bin/sh
# llm-gateway installer (docs/install.md). It checks what it needs itself, downloads the
# gateway for this machine from a GitHub release, verifies its SHA256 and hands over to
# `gateway install`, which shows its plan and asks before it changes anything.
#
#   curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
#   … | sh -s -- --check            preflight only; writes nothing
#   … | sh -s -- --dry-run          show every file it would write; writes nothing
#   … | sh -s -- --install-deps     also install missing dependencies without asking
#   … | sh -s -- 0.3.0 --yes        a given release, no questions
#
# Every flag after an optional version is passed to `gateway install` (gateway install
# --help lists them). LLM_GATEWAY_BASE_URL replaces the release URL: the folder that
# holds SHA256SUMS and the archives (for testing against a local server).
set -eu

REPO="brutally-Honest/llm-gateway"
DOCS="https://github.com/$REPO/blob/main/docs/install.md"

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

version=""
case "${1-}" in
"" | -*) ;;
*)
	version=${1#v}
	shift
	;;
esac

# --- preflight: what this script needs before the gateway binary exists ----------------
missing=0
row() { # mark name detail
	printf '  %s %-18s %s\n' "$1" "$2" "$3"
}
hint() {
	missing=1
	printf '      → %s\n' "$1"
}

printf 'install.sh preflight\n'
os=$(uname -s)
arch=$(uname -m)
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) os="" ;;
esac
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) arch="" ;;
esac
if [ -n "$os" ] && [ -n "$arch" ]; then
	row "✓" "OS and CPU" "$os/$arch"
else
	row "✗" "OS and CPU" "$(uname -s)/$(uname -m)"
	hint "the gateway runs on Linux and macOS, on amd64 or arm64"
fi

pkg_hint() { # package
	if [ "$os" = darwin ]; then
		printf 'install it (with Homebrew: brew install %s), then run this again' "$1"
	else
		printf 'install the %s package with your package manager (for example: sudo apt-get install %s), then run this again' "$1" "$1"
	fi
}

fetcher=""
if command -v curl >/dev/null 2>&1; then
	fetcher=curl
	row "✓" "curl or wget" "$(command -v curl)"
elif command -v wget >/dev/null 2>&1; then
	fetcher=wget
	row "✓" "curl or wget" "$(command -v wget)"
else
	row "✗" "curl or wget" "not found"
	hint "$(pkg_hint curl)"
fi

if command -v tar >/dev/null 2>&1; then
	row "✓" "tar" "$(command -v tar)"
else
	row "✗" "tar" "not found"
	hint "$(pkg_hint tar)"
fi

sumtool=""
if command -v sha256sum >/dev/null 2>&1; then
	sumtool=sha256sum
	row "✓" "checksum tool" "$(command -v sha256sum)"
elif command -v shasum >/dev/null 2>&1; then
	sumtool=shasum
	row "✓" "checksum tool" "$(command -v shasum) -a 256"
else
	row "✗" "checksum tool" "neither sha256sum nor shasum found"
	hint "$(pkg_hint coreutils)"
fi
printf '\n'
[ "$missing" = 0 ] || die "a requirement above is missing; nothing was downloaded. Requirements: $DOCS#requirements"

# --- download and verify ----------------------------------------------------------------
fetch() { # url file
	if [ "$fetcher" = curl ]; then
		curl -fsSL -o "$2" "$1"
	else
		wget -q -O "$2" "$1"
	fi
}

sha256() { # file
	if [ "$sumtool" = sha256sum ]; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

if [ -n "${LLM_GATEWAY_BASE_URL-}" ]; then
	base=${LLM_GATEWAY_BASE_URL%/}
elif [ -n "$version" ]; then
	base="https://github.com/$REPO/releases/download/v$version"
else
	base="https://github.com/$REPO/releases/latest/download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t llm-gateway)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM

printf 'Downloading SHA256SUMS from %s\n' "$base"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "cannot download $base/SHA256SUMS"

# The archive's name comes from SHA256SUMS: llm-gateway_<version>_<os>_<arch>.tar.gz.
line=$(grep "_${os}_${arch}\.tar\.gz\$" "$tmp/SHA256SUMS" | head -n 1 || true)
[ -n "$line" ] || die "this release has no build for $os/$arch"
want=${line%% *}
file=${line##* }
file=${file#\*}
case "$file" in
llm-gateway_*_"${os}_${arch}".tar.gz) ;;
*) die "unexpected file name in SHA256SUMS: $file" ;;
esac
got_version=${file#llm-gateway_}
got_version=${got_version%_"${os}_${arch}".tar.gz}
if [ -n "$version" ] && [ -z "${LLM_GATEWAY_BASE_URL-}" ] && [ "$got_version" != "$version" ]; then
	die "asked for $version, the release holds $got_version"
fi

printf 'Downloading %s\n' "$file"
fetch "$base/$file" "$tmp/$file" || die "cannot download $base/$file"
got=$(sha256 "$tmp/$file")
[ "$got" = "$want" ] || die "checksum mismatch for $file (want $want, got $got); nothing was installed"
printf 'Checksum ok (sha256 %s)\n\n' "$got"

mkdir "$tmp/x"
tar -xzf "$tmp/$file" -C "$tmp/x" gateway || die "cannot unpack $file"
chmod 0755 "$tmp/x/gateway"

# --- hand over -------------------------------------------------------------------------
# `gateway install` copies itself to ~/.local/bin as the first step of its plan, so
# --check, --dry-run or a "no" at its question leave nothing behind. Its questions need
# the terminal: under `curl … | sh` this script's stdin is the script itself.
status=0
if [ -t 0 ]; then
	"$tmp/x/gateway" install "$@" || status=$?
elif (: </dev/tty) 2>/dev/null; then
	"$tmp/x/gateway" install "$@" </dev/tty || status=$?
else
	"$tmp/x/gateway" install "$@" </dev/null || status=$?
fi
exit "$status"
