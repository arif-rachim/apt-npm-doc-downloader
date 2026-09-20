#!/usr/bin/env bash
#
# verify.sh - re-hash a mirror after a USB transfer, before pushing.
#
#   ./verify.sh --bundle /srv/airgap-mirror
#
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/common.sh
. "$SCRIPT_DIR/lib/common.sh"

BUNDLE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --bundle) BUNDLE=${2:?--bundle needs a directory}; shift 2 ;;
    -h|--help) printf 'usage: verify.sh [--bundle DIR]\n'; exit 0 ;;
    *) die "unknown argument $1" ;;
  esac
done

if [ -z "$BUNDLE" ]; then
  # When the scripts were shipped inside a bundle, its root is one level up.
  for candidate in "$SCRIPT_DIR/.." "./bundle" "$SCRIPT_DIR/../bundle" "$PWD"; do
    if [ -f "$candidate/MANIFEST.tsv" ]; then BUNDLE=$(cd "$candidate" && pwd); break; fi
  done
fi
[ -n "$BUNDLE" ] || die "no bundle found; pass --bundle DIR"
BUNDLE=$(cd "$BUNDLE" && pwd)
[ -f "$BUNDLE/MANIFEST.tsv" ] || die "$BUNDLE/MANIFEST.tsv not found"

need_cmd sha256sum
need_cmd awk

step "verifying $BUNDLE"

# SHA256SUMS is regenerable; rebuild it from the manifest so a merged mirror
# is always checked against its own records.
awk -F'\t' 'NF>=6 {printf "%s  %s\n", $2, $4}' "$BUNDLE/MANIFEST.tsv" > "$BUNDLE/SHA256SUMS"

total=$(wc -l < "$BUNDLE/SHA256SUMS")
missing=0
while IFS=$'\t' read -r _eco _sha _size relpath _rest <&3; do
  [ -n "$relpath" ] || continue
  if [ ! -f "$BUNDLE/$relpath" ]; then
    fail "missing $relpath"
    missing=$((missing + 1))
  fi
done 3< "$BUNDLE/MANIFEST.tsv"

bad=0
if ! (cd "$BUNDLE" && sha256sum --quiet -c SHA256SUMS 2>/dev/null); then
  bad=$(cd "$BUNDLE" && sha256sum -c SHA256SUMS 2>/dev/null | grep -c ': FAILED$' || true)
fi

info ""
info "artifacts: $total   missing: $missing   corrupt: $bad"
if [ "$missing" -gt 0 ] || [ "$bad" -gt 0 ]; then
  die "verification failed; re-copy the affected files before pushing"
fi
info "mirror is intact"
