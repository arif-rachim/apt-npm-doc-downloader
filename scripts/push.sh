#!/usr/bin/env bash
#
# push.sh - upload an airgapkit bundle to Sonatype Nexus Repository 3.
#
# Runs entirely offline against the Nexus instance inside the air-gapped
# network: it needs bash and curl, nothing else. Uploads are incremental, so
# dropping a new delta into the same mirror and re-running only pushes what
# is new.
#
#   ./push.sh                 # everything, asking for Nexus details
#   ./push.sh apt npm         # only these ecosystems
#   ./push.sh --dry-run       # show what would happen
#   ./push.sh --retry-failed  # retry artifacts that failed earlier
#
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/common.sh
. "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/components.sh
. "$SCRIPT_DIR/lib/components.sh"
# shellcheck source=lib/docker.sh
. "$SCRIPT_DIR/lib/docker.sh"

usage() {
  cat <<'EOF'
Usage: push.sh [apt|npm|pypi|docker ...] [options]

Options:
  --bundle DIR     bundle/mirror directory (default: ./bundle, or the bundle
                   next to this script)
  --config FILE    settings file (default: ~/.nexus-push.conf)
  --dry-run        list what would be uploaded, upload nothing
  --retry-failed   accepted for compatibility; failed artifacts are retried
                   automatically now
  --reconfigure    ignore the saved settings and ask for them again
  --force          push everything again, ignoring what the local state says
                   was already uploaded. Whether the server replaces an
                   existing artifact depends on the repository's deployment
                   policy: set it to "Allow redeploy" in Nexus to overwrite
  --yes            never prompt; take everything from the config file or the
                   environment (NEXUS_URL, NEXUS_USER, NEXUS_PASS, ...)
  -h, --help       this text

Nexus expectations:
  * apt/npm/pypi hosted repositories reachable at $NEXUS_URL
  * a docker hosted repository; either give its host:port connector as
    NEXUS_DOCKER_HOST, or leave it empty to use
    $NEXUS_URL/repository/<repo>/v2 (path based routing, Nexus 3.83+)
EOF
}

ECOS=()
BUNDLE=""

while [ $# -gt 0 ]; do
  case "$1" in
    apt|npm|pypi|docker) ECOS+=("$1"); shift ;;
    all) ECOS=(apt npm pypi docker); shift ;;
    --bundle) BUNDLE=${2:?--bundle needs a directory}; shift 2 ;;
    --config) CONFIG_FILE=${2:?--config needs a file}; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --retry-failed) RETRY_FAILED=1; shift ;;   # kept for compatibility: retrying is now the default
    --reconfigure) RECONFIGURE=1; shift ;;
    --force) FORCE=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      printf 'error: unknown argument %s\n\n' "$1" >&2
      printf 'These scripts are a copy written into the bundle by airgap %s.\n' "$(script_version)" >&2
      printf 'If the option exists in a newer airgap, refresh this copy with:\n' >&2
      printf '  airgap scripts -out %s\n\n' "${BUNDLE:-<bundle-dir>}" >&2
      printf 'Run push.sh --help for the options this copy supports.\n' >&2
      exit 2
      ;;
  esac
done

need_cmd curl
need_cmd awk
need_cmd mktemp
curl_conf_init

if [ -z "$BUNDLE" ]; then
  # When the scripts were shipped inside a bundle, its root is one level up.
  for candidate in "$SCRIPT_DIR/.." "./bundle" "$SCRIPT_DIR/../bundle" "$PWD"; do
    if [ -f "$candidate/MANIFEST.tsv" ]; then BUNDLE=$(cd "$candidate" && pwd); break; fi
  done
fi
[ -n "$BUNDLE" ] || die "no bundle found; pass --bundle DIR"
[ -f "$BUNDLE/MANIFEST.tsv" ] || die "$BUNDLE/MANIFEST.tsv not found; that directory is not a bundle"
BUNDLE=$(cd "$BUNDLE" && pwd)

if [ ${#ECOS[@]} -eq 0 ]; then
  # Default to whatever the bundle actually contains.
  for eco in apt npm pypi; do
    if awk -F'\t' -v e="$eco" '$1==e {found=1; exit} END {exit !found}' "$BUNDLE/MANIFEST.tsv"; then
      ECOS+=("$eco")
    fi
  done
  [ -f "$BUNDLE/docker/IMAGES.tsv" ] && ECOS+=("docker")
fi
[ ${#ECOS[@]} -gt 0 ] || die "the bundle is empty"

info "push.sh:     airgap $(script_version)"
info "bundle:      $BUNDLE"
info "ecosystems:  ${ECOS[*]}"
[ "$DRY_RUN" = 1 ] && info "mode:        dry run (nothing will be uploaded)"
[ "$FORCE" = 1 ] && info "mode:        force (re-uploading everything, local state ignored)"
info ""

RECONFIGURE=${RECONFIGURE:-0}
if [ "$RECONFIGURE" = 1 ]; then
  rm -f "$CONFIG_FILE"
  unset NEXUS_URL NEXUS_USER NEXUS_PASS
  unset NEXUS_REPO_APT NEXUS_REPO_NPM NEXUS_REPO_PYPI NEXUS_REPO_DOCKER NEXUS_DOCKER_HOST
fi

load_config
had_config=0
[ -f "$CONFIG_FILE" ] && had_config=1
prompt_config "${ECOS[@]}"
NEXUS_URL=${NEXUS_URL%/}

# Check the repository names before uploading anything: a wrong name would
# otherwise fail once per artifact with a bare 404 from the Nexus REST layer.
# This runs before the settings are saved, so what gets written to disk is a
# name that actually exists.
curl_conf_set user "$NEXUS_USER:$NEXUS_PASS"
REPO_CHANGED=0
for eco in "${ECOS[@]}"; do
  case "$eco" in
    apt)    ensure_repo NEXUS_REPO_APT apt ;;
    npm)    ensure_repo NEXUS_REPO_NPM npm ;;
    pypi)   ensure_repo NEXUS_REPO_PYPI pypi ;;
    docker) ensure_repo NEXUS_REPO_DOCKER docker ;;
  esac
done

if [ "$DRY_RUN" != 1 ] && { [ "$had_config" = 0 ] || [ "$REPO_CHANGED" = 1 ]; }; then
  save_config
fi

for eco in "${ECOS[@]}"; do
  case "$eco" in
    apt|npm|pypi) push_components "$eco" ;;
    docker)       push_docker ;;
  esac
done

summary
