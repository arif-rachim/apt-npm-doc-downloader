#!/usr/bin/env bash
#
# merge.sh - fold a delta bundle into the permanent local mirror.
#
#   ./merge.sh /media/usb/bundle-delta /srv/airgap-mirror
#
# Artifacts are content addressed, so copying is idempotent. Generated
# metadata (apt indexes, the PyPI simple index, OCI layout sidecars) is
# overwritten because the delta was produced from a superset mirror on the
# online side.
#
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/common.sh
. "$SCRIPT_DIR/lib/common.sh"

[ $# -eq 2 ] || die "usage: merge.sh SOURCE_DELTA_DIR DEST_MIRROR_DIR"
SRC=$1
DST=$2

need_cmd tar
need_cmd awk
need_cmd sort

[ -f "$SRC/MANIFEST.tsv" ] || die "$SRC/MANIFEST.tsv not found; that is not a bundle"
SRC=$(cd "$SRC" && pwd)
mkdir -p "$DST"
DST=$(cd "$DST" && pwd)
[ "$SRC" != "$DST" ] || die "source and destination are the same directory"

step "merging $SRC into $DST"

before=0
[ -f "$DST/MANIFEST.tsv" ] && before=$(wc -l < "$DST/MANIFEST.tsv")

# 1. Copy everything except the files that need merging rather than replacing.
tar -cf - -C "$SRC" \
  --exclude=./MANIFEST.tsv \
  --exclude=./SHA256SUMS \
  --exclude=./.push-state \
  --exclude=./docker/IMAGES.tsv \
  . | tar -xf - -C "$DST"

# 2. Merge the manifest, newest entry per path wins.
tmp=$(mktemp)
if [ -f "$DST/MANIFEST.tsv" ]; then
  awk -F'\t' 'NF>=6 && !seen[$4]++' "$SRC/MANIFEST.tsv" "$DST/MANIFEST.tsv" > "$tmp"
else
  awk -F'\t' 'NF>=6 && !seen[$4]++' "$SRC/MANIFEST.tsv" > "$tmp"
fi
sort -t$'\t' -k4,4 "$tmp" -o "$tmp"
mv "$tmp" "$DST/MANIFEST.tsv"

# 3. Merge the container image catalogue, keyed by image reference.
if [ -f "$SRC/docker/IMAGES.tsv" ]; then
  mkdir -p "$DST/docker"
  tmp=$(mktemp)
  if [ -f "$DST/docker/IMAGES.tsv" ]; then
    awk -F'\t' 'NF>=5 && !seen[$1]++' "$SRC/docker/IMAGES.tsv" "$DST/docker/IMAGES.tsv" > "$tmp"
  else
    cp "$SRC/docker/IMAGES.tsv" "$tmp"
  fi
  sort -t$'\t' -k1,1 "$tmp" -o "$tmp"
  mv "$tmp" "$DST/docker/IMAGES.tsv"
fi

# 4. Regenerate SHA256SUMS from the merged manifest.
awk -F'\t' 'NF>=6 {printf "%s  %s\n", $2, $4}' "$DST/MANIFEST.tsv" > "$DST/SHA256SUMS"

after=$(wc -l < "$DST/MANIFEST.tsv")
added=$((after - before))
info ""
info "mirror now holds $after artifacts (+$added from this delta)"
info "next: $SCRIPT_DIR/verify.sh --bundle $DST   then   $SCRIPT_DIR/push.sh --bundle $DST"
