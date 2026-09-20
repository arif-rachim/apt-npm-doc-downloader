#!/usr/bin/env bash
#
# build.sh - build, test and package airgapkit.
#
# Needs nothing but bash and the Go toolchain: no make, no network. The
# binary is static (CGO disabled) so it runs on any Ubuntu 24.04 machine, and
# the source has no external Go modules, so this also works inside the
# air-gapped network with GOPROXY off.
#
#   ./build.sh            build the binary
#   ./build.sh test       unit tests
#   ./build.sh check      vet + gofmt + tests
#   ./build.sh offline    prove it builds with no network and empty caches
#   ./build.sh dist       binary + source tarball + SHA256SUMS in ./dist
#   ./build.sh clean
#
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

BINARY=airgap
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X main.version=$VERSION -X main.buildDate=$DATE"

have_go() {
  command -v go >/dev/null 2>&1 || {
    echo "error: the Go toolchain is not on PATH" >&2
    exit 1
  }
}

do_build() {
  have_go
  CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$BINARY" ./cmd/airgap
  ls -lh "$BINARY" | awk '{print "built " $9 " (" $5 ")  version '"$VERSION"'"}'
}

do_test() {
  have_go
  go test ./...
}

do_check() {
  have_go
  go vet ./...
  local unformatted
  unformatted=$(gofmt -l . || true)
  if [ -n "$unformatted" ]; then
    echo "gofmt needed for:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  do_test
  # The push scripts ship inside every bundle, so a syntax error there would
  # only surface on the air-gapped side, where it is expensive to fix.
  local sh
  for sh in scripts/*.sh scripts/lib/*.sh test/*.sh; do
    bash -n "$sh"
  done
  # An end-to-end push against a registry mounted under a path prefix, which
  # is how Nexus serves docker repositories.
  ./test/docker-push-test.sh
  echo "vet, gofmt, tests and shell syntax all clean"
}

do_offline() {
  have_go
  rm -rf .offline
  mkdir -p .offline/mod .offline/cache
  GOPROXY=off GOFLAGS=-mod=mod \
    GOMODCACHE="$PWD/.offline/mod" GOCACHE="$PWD/.offline/cache" \
    CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o .offline/"$BINARY" ./cmd/airgap
  local mods
  mods=$(find .offline/mod -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
  echo "offline build ok with GOPROXY=off and empty caches; external modules needed: $mods"
  rm -rf .offline
}

do_dist() {
  do_build
  rm -rf dist
  mkdir -p dist
  cp "$BINARY" dist/
  # Ship the source too: the air-gapped side may want to rebuild the binary
  # itself rather than trust one that crossed the gap.
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git rev-parse HEAD >/dev/null 2>&1; then
    git archive --format=tar.gz --prefix="airgapkit-$VERSION/" -o "dist/airgapkit-$VERSION-src.tar.gz" HEAD
  else
    tar --exclude=./dist --exclude=./bundle --exclude=./.git --exclude=./.offline \
        --exclude="./$BINARY" --transform "s,^\.,airgapkit-$VERSION," \
        -czf "dist/airgapkit-$VERSION-src.tar.gz" .
  fi
  ( cd dist && sha256sum ./* > SHA256SUMS )
  echo
  ls -lh dist | tail -n +2 | awk '{print "  " $9 "  " $5}'
}

case "${1:-build}" in
  build)   do_build ;;
  test)    do_test ;;
  check)   do_check ;;
  offline) do_offline ;;
  dist)    do_dist ;;
  clean)   rm -rf "$BINARY" dist .offline; echo "cleaned" ;;
  *)       sed -n '3,16p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
