#!/usr/bin/env bash
# Regenerate ./.xray-core-patched — a local, patched copy of xray-core that adds
# outbound client-certificate (mutual-TLS) support, which upstream xray-core does
# not yet provide (it only wires configured certs into the server-side
# GetCertificate, and its uTLS copyConfig drops cert fields). The tw relay's Caddy
# enforces `client_auth require_and_verify`, so the tunnel must present a client
# cert on outbound TLS. Temporary until upstream ships native mTLS.
#
# Committed (tiny):   this script + scripts/xray-core-client-cert.patch
# Generated (ignored): ./.xray-core-patched (~6.5 MB), referenced by the
#                      `replace github.com/xtls/xray-core => ./.xray-core-patched`
#                      directive in go.mod.
#
# Run `make patch-xray` (or this script) once after cloning, and again whenever
# the patch or the pinned VERSION changes. Bump VERSION to match the require line
# in go.mod when upgrading xray-core.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="v1.260206.0"   # keep in sync with the github.com/xtls/xray-core require in go.mod
DST=".xray-core-patched"
PATCH="scripts/xray-core-client-cert.patch"

# GOTOOLCHAIN=auto so this works regardless of the caller's environment (the
# Makefile pins GOTOOLCHAIN=local, and go.mod requires a newer toolchain).
echo "==> ensuring xray-core ${VERSION} is in the module cache"
GOTOOLCHAIN=auto GOFLAGS=-mod=mod go mod download "github.com/xtls/xray-core@${VERSION}"
SRC="$(go env GOMODCACHE)/github.com/xtls/xray-core@${VERSION}"
if [ ! -d "${SRC}" ]; then
	echo "error: ${SRC} not found after download" >&2
	exit 1
fi

echo "==> regenerating ${DST} from ${SRC}"
rm -rf "${DST}"
mkdir -p "${DST}"
cp -a "${SRC}/." "${DST}/"
chmod -R u+w "${DST}"

echo "==> applying ${PATCH}"
patch -p1 -d "${DST}" < "${PATCH}"

touch "${DST}/.tw-patched"
echo "==> done: ${DST} ready (xray-core ${VERSION} + client-cert mTLS patch)"
