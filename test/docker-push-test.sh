#!/usr/bin/env bash
#
# Pushes a tiny synthetic image through push.sh against a registry mounted
# under a path prefix, the way Nexus serves docker repositories. It failed
# with "blob PUT returned HTTP 405" before the Location handling was fixed.
#
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
ROOT=$PWD

command -v python3 >/dev/null 2>&1 || { echo "docker-push-test: python3 missing, skipped"; exit 0; }

PORT=${PORT:-18091}
WORK=$(mktemp -d)
cleanup() {
  [ -n "${MOCK_PID:-}" ] && kill "$MOCK_PID" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

# A bundle with one image: a config blob and one layer, a few bytes each.
mkdir -p "$WORK/bundle/docker/blobs/sha256" "$WORK/bundle/docker/images/docker.io/library/demo/1.0"
: > "$WORK/bundle/MANIFEST.tsv"
IMG=$WORK/bundle/docker/images/docker.io/library/demo/1.0
make_blob() {
  printf '%s' "$1" > "$WORK/blob.tmp"
  sha=$(sha256sum "$WORK/blob.tmp" | cut -d' ' -f1)
  mv "$WORK/blob.tmp" "$WORK/bundle/docker/blobs/sha256/$sha"
  printf '%s' "$sha"
}
CFG=$(make_blob '{"architecture":"amd64","os":"linux"}')
LAYER=$(make_blob 'not-a-real-layer')
{
  printf 'sha256:%s\t38\tdocker/blobs/sha256/%s\tapplication/vnd.oci.image.config.v1+json\n' "$CFG" "$CFG"
  printf 'sha256:%s\t16\tdocker/blobs/sha256/%s\tapplication/vnd.oci.image.layer.v1.tar+gzip\n' "$LAYER" "$LAYER"
} > "$IMG/blobs.tsv"
printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}' > "$IMG/manifest.json"
printf 'docker.io/library/demo:1.0\tdocker/images/docker.io/library/demo/1.0/manifest.json\tapplication/vnd.oci.image.manifest.v1+json\tsha256:deadbeef\tdocker/images/docker.io/library/demo/1.0/blobs.tsv\n' \
  > "$WORK/bundle/docker/IMAGES.tsv"

python3 test/mock-registry.py "$PORT" &
MOCK_PID=$!
for _ in $(seq 1 40); do
  curl -sS -o /dev/null "http://127.0.0.1:$PORT/repository/docker-hosted/v2/" 2>/dev/null && break
  sleep 0.25
done

out=$(NEXUS_URL="http://127.0.0.1:$PORT" NEXUS_USER=t NEXUS_PASS=t \
      NEXUS_REPO_DOCKER=docker-hosted CONFIG_FILE="$WORK/conf" \
      "$ROOT/scripts/push.sh" docker --bundle "$WORK/bundle" --yes 2>&1) || true

stored=$(curl -sS "http://127.0.0.1:$PORT/count")
if [ "$stored" != "2" ] || ! printf '%s' "$out" | grep -q 'failed 0'; then
  printf '%s\n' "$out"
  echo "docker-push-test: FAILED (registry stored $stored/2 blobs)"
  exit 1
fi
echo "docker-push-test: ok (2 blobs and the manifest reached a path-mounted registry)"
