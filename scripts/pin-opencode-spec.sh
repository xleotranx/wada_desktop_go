#!/bin/sh
# Pin the OpenAPI document of the OpenCode release the app runs, for
# TestClientSpeaksThePinnedSpec. Usage: scripts/pin-opencode-spec.sh [opencode]
#
# Starts the given opencode (default: the one on PATH) on a free port with
# throwaway config and data folders, and saves its /openapi.json as
# internal/plugins/opencode/testdata/openapi-<version>.json. The binary must
# be the version internal/plugins/opencode/install.go pins.
set -eu

bin=${1:-opencode}
root=$(cd "$(dirname "$0")/.." && pwd)
want=$(sed -n 's/^const version = "\(.*\)"$/\1/p' "$root/internal/plugins/opencode/install.go")
have=$("$bin" --version | sed 's/^opencode v//')
if [ "$have" != "$want" ]; then
	echo "$bin is $have; the app pins $want" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$tmp"' EXIT
mkdir -p "$tmp/config" "$tmp/data" "$tmp/state" "$tmp/cache" "$tmp/work"
port=$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
(cd "$tmp/work" && XDG_CONFIG_HOME="$tmp/config" XDG_DATA_HOME="$tmp/data" XDG_STATE_HOME="$tmp/state" \
	XDG_CACHE_HOME="$tmp/cache" exec "$bin" serve --hostname 127.0.0.1 --port "$port" >"$tmp/log" 2>&1) &
pid=$!

i=0
until password=$(sed -n 's/^server password //p' "$tmp/log") && [ -n "$password" ]; do
	i=$((i + 1))
	if [ "$i" -gt 50 ]; then
		echo "the server did not start:" >&2
		cat "$tmp/log" >&2
		exit 1
	fi
	sleep 0.2
done

out="$root/internal/plugins/opencode/testdata/openapi-$want.json"
curl -sf -u "opencode:$password" "http://127.0.0.1:$port/openapi.json" -o "$out"
echo "pinned $out"
