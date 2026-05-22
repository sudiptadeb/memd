#!/usr/bin/env bash
# Build script for memd.
#
# Usage:
#   build/build.sh           build for the host OS/arch
#   build/build.sh all       cross-compile for darwin/linux x amd64/arm64
#   build/build.sh clean     remove the dist/ directory
#
# Env:
#   VERSION    override the version string (default 0.1.0-dev)

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0-dev}"
PACKAGE="./server/cmd/memd"
BINARY="memd"
DIST_DIR="dist"
LDFLAGS="-s -w -X github.com/sudiptadeb/memd/server/internal/version.Value=${VERSION}"

sync_doctrine() {
  # Mirror the canonical doctrine into the embedded copy so go:embed picks
  # up the latest text. The two files must stay byte-identical.
  if [[ ! -f docs/doctrine.md ]]; then
    return
  fi
  cp docs/doctrine.md server/internal/doctrine/doctrine.md
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
    echo "Removing ${DIST_DIR}/"
    rm -rf "${DIST_DIR}"
    exit 0
    ;;
  all)
    sync_doctrine
    build_one darwin arm64
    build_one darwin amd64
    build_one linux  arm64
    build_one linux  amd64
    ;;
  host)
    sync_doctrine
    build_one "$(go env GOOS)" "$(go env GOARCH)"
    ;;
  *)
    echo "unknown target: ${TARGET}" >&2
    echo "usage: build/build.sh [all|clean]" >&2
    exit 2
    ;;
esac

echo
echo "Built version ${VERSION}:"
find "${DIST_DIR}" -type f | sort
