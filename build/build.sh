#!/usr/bin/env bash
# Build script for memd.
#
# Usage:
#   build/build.sh           build for the host OS/arch
#   build/build.sh all       cross-compile for darwin/linux x amd64/arm64
#   build/build.sh clean     remove build output
#
# Env:
#   VERSION    override the version string (default 0.1.0-dev)
#   SKIP_WEB   set to 1 to skip the web UI build (binary embeds whatever
#              server/internal/ui/dist already holds)

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0-dev}"
PACKAGE="./server/cmd/memd"
BINARY="memd"
DIST_DIR="dist"
WEB_DIR="web"
UI_DIST="server/internal/ui/dist"
LDFLAGS="-s -w -X github.com/sudiptadeb/memd/server/internal/version.Value=${VERSION}"

sync_doctrine() {
  # Mirror the canonical doctrine into the embedded copy so go:embed picks
  # up the latest text. The two files must stay byte-identical.
  if [[ ! -f docs/doctrine.md ]]; then
    return
  fi
  cp docs/doctrine.md server/internal/doctrine/doctrine.md
}

# Build the Vue single-page apps into server/internal/ui/dist/<app>, which the Go
# binary embeds. Each app is built independently (VITE_APP) so it stays
# self-contained and independently embeddable.
build_web() {
  if [[ "${SKIP_WEB:-0}" == "1" ]]; then
    echo "SKIP_WEB=1 set; skipping web UI build"
    return
  fi
  if [[ ! -f "${WEB_DIR}/package.json" ]]; then
    return
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "WARNING: npm not found; skipping web UI build. The embedded UI will be" >&2
    echo "         whatever already exists under ${UI_DIST}. Install Node.js to" >&2
    echo "         build the current frontend." >&2
    return
  fi
  echo "→ web UI: npm ci"
  ( cd "${WEB_DIR}" && npm ci )
  for app in dashboard admin; do
    echo "→ web UI: building ${app}"
    ( cd "${WEB_DIR}" && VITE_APP="${app}" npm run build )
  done
}

build_one() {
  local goos="$1"
  local goarch="$2"
  local outdir="${DIST_DIR}/${goos}"
  local outfile="${outdir}/${BINARY}-${goarch}-v${VERSION}"

  mkdir -p "${outdir}"
  echo "→ ${goos}/${goarch}  →  ${outfile}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -trimpath -ldflags="${LDFLAGS}" -o "${outfile}" "${PACKAGE}"
}

TARGET="${1:-host}"

case "${TARGET}" in
  clean)
    echo "Removing ${DIST_DIR}/ and ${UI_DIST}/"
    rm -rf "${DIST_DIR}" "${UI_DIST}"
    exit 0
    ;;
  all)
    sync_doctrine
    build_web
    build_one darwin arm64
    build_one darwin amd64
    build_one linux  arm64
    build_one linux  amd64
    ;;
  host)
    sync_doctrine
    build_web
    build_one "$(go env GOOS)" "$(go env GOARCH)"
    ;;
  *)
    echo "unknown target: ${TARGET}" >&2
    echo "usage: build/build.sh [host|all|clean]" >&2
    exit 2
    ;;
esac

echo
echo "Built version ${VERSION}:"
find "${DIST_DIR}" -type f | sort
