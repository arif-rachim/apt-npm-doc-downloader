# shellcheck shell=bash
# Uploads apt, npm and PyPI artifacts through the Nexus Components API.
# All three formats use the same endpoint and differ only in the multipart
# field name, so one implementation covers them.

# component_field maps an ecosystem to its Nexus upload field.
component_field() {
  case "$1" in
    apt)  printf 'apt.asset' ;;
    npm)  printf 'npm.asset' ;;
    pypi) printf 'pypi.asset' ;;
    *)    die "unknown component ecosystem $1" ;;
  esac
}

component_repo() {
  case "$1" in
    apt)  printf '%s' "${NEXUS_REPO_APT:-}" ;;
    npm)  printf '%s' "${NEXUS_REPO_NPM:-}" ;;
    pypi) printf '%s' "${NEXUS_REPO_PYPI:-}" ;;
  esac
}

# push_components ECOSYSTEM
#
# Reads MANIFEST.tsv, uploads every artifact of that ecosystem which the state
# file does not already record as successfully pushed.
push_components() {
  local eco=$1 repo field manifest
  repo=$(component_repo "$eco")
  field=$(component_field "$eco")
  [ -n "$repo" ] || die "no Nexus repository configured for $eco"
  manifest="$BUNDLE/MANIFEST.tsv"
  [ -f "$manifest" ] || die "$manifest not found; is $BUNDLE a bundle directory?"

  state_init "$repo"
  curl_conf_set user "$NEXUS_USER:$NEXUS_PASS"
  step "== $eco -> $NEXUS_URL/repository/$repo =="

  local url="$NEXUS_URL/service/rest/v1/components?repository=$repo"
  local sha size relpath total=0 n=0 before_pushed=$PUSHED before_skipped=$SKIPPED before_failed=$FAILED

  total=$(awk -F'\t' -v e="$eco" '$1==e {c++} END {print c+0}' "$manifest")
  [ "$total" -gt 0 ] || { info "  nothing to push"; return 0; }

  # Read through fd 3 so curl inside the loop cannot swallow the manifest.
  while IFS=$'\t' read -r e sha size relpath _rest <&3; do
    [ "$e" = "$eco" ] || continue
    n=$((n + 1))
    local file="$BUNDLE/$relpath"
    if [ ! -f "$file" ]; then
      fail "$relpath (missing from bundle)"
      FAILED=$((FAILED + 1))
      continue
    fi
    if ! should_push "$repo" "$sha"; then
      SKIPPED=$((SKIPPED + 1))
      continue
    fi
    if [ "$DRY_RUN" = 1 ]; then
      info "  would upload [$n/$total] $relpath"
      PUSHED=$((PUSHED + 1))
      continue
    fi

    local body code
    body=$(mktemp)
    code=$(norm_code "$(curl -sS -o "$body" -w '%{http_code}' \
      --retry 3 --retry-delay 2 --retry-connrefused \
      -K "$CURL_CONF" \
      -H 'Expect:' \
      -F "$field=@$file" \
      "$url" || true)")

    case "$code" in
      204|201|200)
        ok "[$n/$total] $relpath"
        state_record "$repo" "$sha" ok "$relpath"
        PUSHED=$((PUSHED + 1))
        ;;
      400)
        # Nexus answers 400 when the component already exists in a hosted
        # repository whose deployment policy forbids redeploy. Without
        # --force that is the normal outcome for an artifact pushed by an
        # earlier batch, so it counts as present. With --force the user
        # explicitly asked to overwrite, so it is a real failure and the
        # policy is what has to change.
        if grep -qiE 'already exists|does not allow updating|repository does not allow' "$body"; then
          if [ "$FORCE" = 1 ]; then
            fail "[$n/$total] $relpath: Nexus refused to replace it"
            REDEPLOY_BLOCKED=1
            state_record "$repo" "$sha" fail "$relpath"
            FAILED=$((FAILED + 1))
          else
            skip "[$n/$total] $relpath (already in Nexus)"
            state_record "$repo" "$sha" ok "$relpath"
            SKIPPED=$((SKIPPED + 1))
          fi
        else
          fail "[$n/$total] $relpath: HTTP 400: $(head -c 300 "$body")"
          state_record "$repo" "$sha" fail "$relpath"
          FAILED=$((FAILED + 1))
        fi
        ;;
      401|403)
        rm -f "$body"
        die "authentication failed for $NEXUS_USER at $NEXUS_URL (HTTP $code)"
        ;;
      404)
        rm -f "$body"
        die "repository \"$repo\" was not found on $NEXUS_URL (HTTP 404 from the Nexus REST API). Check the name with: curl -u $NEXUS_USER $NEXUS_URL/service/rest/v1/repositories"
        ;;
      *)
        fail "[$n/$total] $relpath: HTTP $code: $(head -c 300 "$body")"
        state_record "$repo" "$sha" fail "$relpath"
        FAILED=$((FAILED + 1))
        ;;
    esac
    rm -f "$body"
  done 3< "$manifest"

  local did=$((PUSHED - before_pushed)) sk=$((SKIPPED - before_skipped)) fl=$((FAILED - before_failed))
  info "  $eco: $did uploaded, $sk already on the server, $fl failed (of $total)"

  if [ "${REDEPLOY_BLOCKED:-0}" = 1 ]; then
    warn "repository \"$repo\" has its deployment policy set to \"Disable redeploy\", so existing artifacts cannot be overwritten."
    warn "  To allow it: Nexus > Administration > Repository > Repositories > $repo > Hosted > Deployment policy > Allow redeploy."
    REDEPLOY_BLOCKED=0
  fi
}
