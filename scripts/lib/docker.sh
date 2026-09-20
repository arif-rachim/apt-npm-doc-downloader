# shellcheck shell=bash
# Pushes OCI images to a Nexus docker (hosted) repository using nothing but
# curl against the Registry v2 API. No Docker daemon is needed on the
# air-gapped machine, and blobs that the registry already has are skipped,
# which is what makes pushing a second version of an image fast.

docker_v2_base() {
  if [ -n "${NEXUS_DOCKER_HOST:-}" ]; then
    case "$NEXUS_DOCKER_HOST" in
      http://*|https://*) printf '%s/v2' "${NEXUS_DOCKER_HOST%/}" ;;
      *)                  printf 'https://%s/v2' "${NEXUS_DOCKER_HOST%/}" ;;
    esac
  else
    printf '%s/repository/%s/v2' "$NEXUS_URL" "$NEXUS_REPO_DOCKER"
  fi
}

# origin_of prints scheme://host[:port] of a URL.
origin_of() {
  printf '%s' "$1" | sed -n 's#^\(https\?://[^/]*\).*#\1#p'
}

# use_basic_auth points curl at the credentials for the rest of the calls.
use_basic_auth() {
  curl_conf_set user "$NEXUS_USER:$NEXUS_PASS"
}

# docker_auth obtains a credential for one repository scope and writes it into
# the curl config file. Nexus normally runs the Docker Bearer Token realm, but
# some instances accept plain basic auth, so both are supported.
docker_auth() {
  local repo=$1 v2=$2 hdrs code www realm service token body

  # Try plain basic auth first: it is one request, and it works on instances
  # where the Docker Bearer Token Realm is not active.
  use_basic_auth
  code=$(norm_code "$(curl -sS -o /dev/null -w '%{http_code}' -K "$CURL_CONF" "$v2/" </dev/null || true)")
  if [ "$code" = "200" ]; then
    return 0
  fi

  hdrs=$(mktemp)
  code=$(norm_code "$(curl -sS -o /dev/null -D "$hdrs" -w '%{http_code}' "$v2/" </dev/null || true)")
  if [ "$code" != "401" ]; then
    rm -f "$hdrs"
    use_basic_auth
    return 0
  fi
  www=$(grep -i '^www-authenticate:' "$hdrs" | head -1 | tr -d '\r')
  rm -f "$hdrs"
  case "$www" in
    *[Bb]earer*)
      realm=$(printf '%s' "$www" | sed -n 's/.*realm="\([^"]*\)".*/\1/p')
      service=$(printf '%s' "$www" | sed -n 's/.*service="\([^"]*\)".*/\1/p')
      [ -n "$realm" ] || die "bearer challenge from $v2 without a realm"
      body=$(mktemp)
      curl_conf_set user "$NEXUS_USER:$NEXUS_PASS"
      # curl without -f exits 0 even on 404, so check the status explicitly:
      # a missing token endpoint is the usual cause and needs its own advice.
      code=$(norm_code "$(curl -sS -o "$body" -w '%{http_code}' -K "$CURL_CONF" -G \
        --data-urlencode "service=$service" \
        --data-urlencode "scope=repository:$repo:pull,push" \
        "$realm" </dev/null || true)")
      token=""
      if [ "$code" = "200" ]; then
        token=$(sed -n 's/.*"\(access_token\|token\)"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\2/p' "$body" | head -1)
      fi
      rm -f "$body"
      if [ -z "$token" ]; then
        printf '\n' >&2
        fail "the registry asked for a bearer token from $realm, but that endpoint answered HTTP $code"
        die "Nexus advertises this realm even when the token service is off. Fix it in Nexus:
           Administration > Security > Realms > move \"Docker Bearer Token Realm\" into Active
         Then retry. (Basic auth was tried first and $NEXUS_URL rejected it as well.)"
      fi
      curl_conf_set header "Authorization: Bearer $token"
      ;;
    *)
      use_basic_auth
      ;;
  esac
}

# target_path maps a mirrored reference onto the Nexus repository path.
# docker.io/library/nginx:1.27 becomes library/nginx:1.27 unless
# NEXUS_DOCKER_KEEP_REGISTRY=yes, which keeps the origin as a path prefix so
# images with the same name from different registries cannot collide.
target_path() {
  local ref=$1 name tag
  case "$ref" in
    *@*) name=${ref%@*}; tag=$(printf '%s' "${ref##*@}" | tr ':' '-') ;;
    *)   name=${ref%:*}; tag=${ref##*:} ;;
  esac
  if [ "${NEXUS_DOCKER_KEEP_REGISTRY:-no}" != "yes" ]; then
    case "$name" in
      */*) name=${name#*/} ;;
    esac
  else
    name=$(printf '%s' "$name" | tr ':' '-')
  fi
  printf '%s\t%s' "$name" "$tag"
}

blob_exists() {
  local v2=$1 name=$2 digest=$3 code
  code=$(norm_code "$(curl -sS -o /dev/null -w '%{http_code}' -I -K "$CURL_CONF" \
    "$v2/$name/blobs/$digest" || true)")
  [ "$code" = "200" ]
}

# upload_blob performs the two step monolithic upload: start a session, then
# PUT the bytes with the digest appended to whatever URL the registry handed
# back. The Location is opaque: it may be relative and may already carry a
# query string.
upload_blob() {
  local v2=$1 name=$2 digest=$3 file=$4 hdrs loc sep code origin
  hdrs=$(mktemp)
  code=$(norm_code "$(curl -sS -o /dev/null -D "$hdrs" -w '%{http_code}' \
    -X POST -K "$CURL_CONF" -H 'Content-Length: 0' -H 'Expect:' \
    "$v2/$name/blobs/uploads/" || true)")
  if [ "$code" != "202" ]; then
    rm -f "$hdrs"
    case "$code" in
      429) printf 'upload refused with HTTP 429: Nexus is rate limiting authentication for this account (it logs "retryAfter=30s"). Wait half a minute and run push.sh again' ;;
      401) printf 'upload refused with HTTP 401: %s accepted the credentials for reading but not for writing. In Nexus: Administration > Security > Realms > activate "Docker Bearer Token Realm"' "$v2" ;;
      405) printf 'upload refused with HTTP 405 at %s/%s/blobs/uploads/ (wrong registry URL; if you are using path based routing, try the repository connector port instead via NEXUS_DOCKER_HOST)' "$v2" "$name" ;;
      *)   printf 'upload session refused (HTTP %s)' "$code" ;;
    esac
    return 1
  fi
  loc=$(grep -i '^location:' "$hdrs" | head -1 | sed 's/^[Ll]ocation:[[:space:]]*//' | tr -d '\r')
  rm -f "$hdrs"
  [ -n "$loc" ] || { printf 'upload session returned no Location'; return 1; }
  # Resolving the Location is where this goes wrong most easily. Nexus
  # reached through path based routing lives at
  # <host>/repository/<repo>/v2, but hands back a Location that is relative
  # to the registry root ("/v2/<name>/blobs/uploads/<uuid>"). Pasting that
  # onto the bare origin produces <host>/v2/... , which is not the registry
  # at all: Nexus answers the PUT with 405 Method Not Allowed.
  case "$loc" in
    http://*|https://*) ;;
    /v2/*) loc="${v2%/v2}$loc" ;;
    /*)    origin=$(origin_of "$v2"); loc="$origin$loc" ;;
    *)     loc="$v2/$name/blobs/uploads/$loc" ;;
  esac
  case "$loc" in
    *\?*) sep='&' ;;
    *)    sep='?' ;;
  esac
  code=$(norm_code "$(curl -sS -o /dev/null -w '%{http_code}' \
    -X PUT -K "$CURL_CONF" -H 'Content-Type: application/octet-stream' -H 'Expect:' \
    --data-binary "@$file" "${loc}${sep}digest=$digest" || true)")
  if [ "$code" != "201" ]; then
    case "$code" in
      405) printf 'blob PUT returned HTTP 405 for %s (the registry does not accept uploads at that URL)' "$loc" ;;
      401) printf 'blob PUT returned HTTP 401 (the registry accepted the upload session but not the write; enable the Docker Bearer Token Realm in Nexus)' ;;
      *)   printf 'blob PUT returned HTTP %s' "$code" ;;
    esac
    return 1
  fi
  return 0
}

push_manifest() {
  local v2=$1 name=$2 tag=$3 mediatype=$4 file=$5 body code
  body=$(mktemp)
  code=$(norm_code "$(curl -sS -o "$body" -w '%{http_code}' \
    -X PUT -K "$CURL_CONF" -H "Content-Type: $mediatype" -H 'Expect:' \
    --data-binary "@$file" "$v2/$name/manifests/$tag" || true)")
  if [ "$code" != "201" ] && [ "$code" != "200" ]; then
    printf 'manifest PUT returned HTTP %s: %s' "$code" "$(head -c 300 "$body")"
    rm -f "$body"
    return 1
  fi
  rm -f "$body"
  return 0
}

push_docker() {
  local images="$BUNDLE/docker/IMAGES.tsv" v2 repo
  repo=${NEXUS_REPO_DOCKER:-}
  [ -n "$repo" ] || die "no Nexus docker repository configured"
  if [ ! -f "$images" ]; then
    info "  no docker images in this bundle"
    return 0
  fi
  v2=$(docker_v2_base)
  state_init "$repo"
  step "== docker -> $v2 =="

  local ref manifest_path mediatype digest blobs_path
  # fd 3 and fd 4 keep stdin free for curl.
  while IFS=$'\t' read -r ref manifest_path mediatype digest blobs_path <&3; do
    [ -n "$ref" ] || continue
    local name tag
    IFS=$'\t' read -r name tag <<< "$(target_path "$ref")"

    if [ "$DRY_RUN" = 1 ]; then
      info "  would push $ref -> $name:$tag"
      PUSHED=$((PUSHED + 1))
      continue
    fi

    docker_auth "$name" "$v2"

    local blob_failed=0 bdigest bsize brel bmt
    while IFS=$'\t' read -r bdigest bsize brel bmt <&4; do
      [ -n "$bdigest" ] || continue
      local key="$name|$bdigest"
      if ! should_push "$repo" "$key"; then
        SKIPPED=$((SKIPPED + 1))
        continue
      fi
      # --force re-uploads blobs instead of trusting what the registry has.
      if [ "$FORCE" != 1 ] && blob_exists "$v2" "$name" "$bdigest"; then
        skip "$name blob ${bdigest:7:12} (already in Nexus)"
        state_record "$repo" "$key" ok "$brel"
        SKIPPED=$((SKIPPED + 1))
        continue
      fi
      local file="$BUNDLE/$brel" err
      if [ ! -f "$file" ]; then
        fail "$name blob ${bdigest:7:12}: $brel missing from bundle"
        FAILED=$((FAILED + 1)); blob_failed=1
        continue
      fi
      if err=$(upload_blob "$v2" "$name" "$bdigest" "$file"); then
        ok "$name blob ${bdigest:7:12} ($bsize bytes)"
        state_record "$repo" "$key" ok "$brel"
        PUSHED=$((PUSHED + 1))
      else
        fail "$name blob ${bdigest:7:12}: $err"
        state_record "$repo" "$key" fail "$brel"
        FAILED=$((FAILED + 1)); blob_failed=1
      fi
    done 4< "$BUNDLE/$blobs_path"

    if [ "$blob_failed" = 1 ]; then
      fail "$ref: manifest not pushed because some blobs failed"
      continue
    fi

    local mkey="$name:$tag|$digest"
    if ! should_push "$repo" "$mkey"; then
      SKIPPED=$((SKIPPED + 1))
      continue
    fi
    if err=$(push_manifest "$v2" "$name" "$tag" "$mediatype" "$BUNDLE/$manifest_path"); then
      ok "$ref -> $name:$tag"
      state_record "$repo" "$mkey" ok "$manifest_path"
      PUSHED=$((PUSHED + 1))
    else
      fail "$ref: $err"
      state_record "$repo" "$mkey" fail "$manifest_path"
      FAILED=$((FAILED + 1))
    fi
  done 3< "$images"
}
