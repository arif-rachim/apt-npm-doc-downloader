# shellcheck shell=bash
# Shared helpers for the air-gapped push scripts. Everything here relies on
# bash and curl only: the air-gapped machine is not assumed to have jq,
# python, docker or any language runtime installed.

CONFIG_FILE="${CONFIG_FILE:-$HOME/.nexus-push.conf}"
DRY_RUN=0
RETRY_FAILED=0
ASSUME_YES=0
FORCE=0

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BOLD=$'\033[1m'; C_OFF=$'\033[0m'
else
  C_RED=''; C_GREEN=''; C_YELLOW=''; C_BOLD=''; C_OFF=''
fi

info()  { printf '%s\n' "$*"; }
step()  { printf '%s%s%s\n' "$C_BOLD" "$*" "$C_OFF"; }
ok()    { printf '  %sOK%s      %s\n' "$C_GREEN" "$C_OFF" "$*"; }
skip()  { printf '  SKIP    %s\n' "$*"; }
warn()  { printf '  %sWARN%s    %s\n' "$C_YELLOW" "$C_OFF" "$*" >&2; }
fail()  { printf '  %sFAIL%s    %s\n' "$C_RED" "$C_OFF" "$*" >&2; }
die()   { printf '%serror:%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }

# script_version reports the airgap build that wrote this copy of the
# scripts, so an outdated bundle is obvious rather than puzzling.
script_version() {
  local stamp="$SCRIPT_DIR/.airgap-version"
  if [ -f "$stamp" ]; then
    # shellcheck disable=SC1090
    . "$stamp"
    printf '%s (content %s)' "${AIRGAP_SCRIPTS_VERSION:-?}" "${AIRGAP_SCRIPTS_SHA256:-?}"
  else
    printf 'source checkout'
  fi
}

# http_code normalises what curl reports: on a connection failure curl both
# prints "000" and exits non-zero, and the old "|| printf 000" idiom appended
# a second one, producing codes like "000000".
norm_code() {
  case "$1" in
    ''|*[!0-9]*) printf '000' ;;
    *) printf '%s' "$1" ;;
  esac
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

# ---------------------------------------------------------------- curl secrets
#
# Credentials must never be passed to curl as arguments: everything in argv is
# readable by any user on the machine through `ps auxww` for as long as the
# request runs, and a large layer upload can run for minutes. curl reads them
# from a 0600 config file instead, so they stay off the process list.

CURL_CONF=""

curl_conf_init() {
  local old_umask
  old_umask=$(umask)
  umask 077
  CURL_CONF=$(mktemp)
  umask "$old_umask"
  trap 'curl_conf_cleanup' EXIT INT TERM
}

curl_conf_cleanup() {
  [ -n "$CURL_CONF" ] && rm -f "$CURL_CONF"
  CURL_CONF=""
}

# curl_conf_set KEY VALUE [KEY VALUE ...] rewrites the config file. Values are
# escaped for curl's double-quoted syntax so passwords containing quotes or
# backslashes survive intact.
curl_conf_set() {
  [ -n "$CURL_CONF" ] || die "curl_conf_init was not called"
  : > "$CURL_CONF"
  while [ $# -ge 2 ]; do
    printf '%s = "%s"\n' "$1" "$(printf '%s' "$2" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')" >> "$CURL_CONF"
    shift 2
  done
}

# ---------------------------------------------------------------- configuration

# load_config reads the saved configuration; environment variables always win.
load_config() {
  if [ -f "$CONFIG_FILE" ]; then
    local perms
    perms=$(stat -c '%a' "$CONFIG_FILE" 2>/dev/null || echo "")
    case "$perms" in
      600|400) ;;
      "") ;;
      *) warn "$CONFIG_FILE is mode $perms; it stores a password, tightening to 600"
         chmod 600 "$CONFIG_FILE" ;;
    esac
    # shellcheck disable=SC1090
    . "$CONFIG_FILE"
  fi
}

ask() {
  # ask VAR "Prompt" "default"
  local var=$1 prompt=$2 default=${3:-} current answer
  eval "current=\${$var:-}"
  [ -n "$current" ] && default=$current
  if [ "$ASSUME_YES" = 1 ]; then
    [ -n "$default" ] || die "$var is not set and --yes was given"
    eval "$var=\$default"
    return
  fi
  while :; do
    if [ -n "$default" ]; then
      printf '%s [%s]: ' "$prompt" "$default" >&2
    else
      printf '%s: ' "$prompt" >&2
    fi
    IFS= read -r answer || true
    [ -z "$answer" ] && answer=$default
    [ -n "$answer" ] && break
    printf '  this one cannot be left empty\n' >&2
  done
  eval "$var=\$answer"
}

# ask_optional is ask() for a setting that may legitimately stay empty, such
# as the docker connector host when path based routing is used.
ask_optional() {
  local var=$1 prompt=$2 default=${3:-} current answer
  eval "current=\${$var:-}"
  [ -n "$current" ] && default=$current
  if [ "$ASSUME_YES" = 1 ]; then
    eval "$var=\$default"
    return
  fi
  if [ -n "$default" ]; then
    printf '%s [%s]: ' "$prompt" "$default" >&2
  else
    printf '%s: ' "$prompt" >&2
  fi
  IFS= read -r answer || true
  [ -z "$answer" ] && answer=$default
  eval "$var=\$answer"
}

ask_secret() {
  local var=$1 prompt=$2 current answer
  eval "current=\${$var:-}"
  if [ -n "$current" ] || [ "$ASSUME_YES" = 1 ]; then
    [ -n "$current" ] || die "$var is not set and --yes was given"
    return
  fi
  printf '%s: ' "$prompt" >&2
  stty -echo 2>/dev/null || true
  IFS= read -r answer || true
  stty echo 2>/dev/null || true
  printf '\n' >&2
  eval "$var=\$answer"
}

prompt_config() {
  while :; do
    ask NEXUS_URL "Nexus base URL (e.g. https://nexus.example.com)"
    NEXUS_URL=${NEXUS_URL%/}
    case "$NEXUS_URL" in
      http://*|https://*) break ;;
      *)
        if [ "$ASSUME_YES" = 1 ]; then
          die "NEXUS_URL must start with http:// or https://, got \"$NEXUS_URL\""
        fi
        printf '  the URL must start with http:// or https://\n' >&2
        NEXUS_URL=""
        ;;
    esac
  done
  ask NEXUS_USER "Nexus username"
  ask_secret NEXUS_PASS "Nexus password"
  local eco
  for eco in "$@"; do
    case "$eco" in
      apt)    ask NEXUS_REPO_APT    "apt hosted repository name"    "apt-hosted" ;;
      npm)    ask NEXUS_REPO_NPM    "npm hosted repository name"    "npm-hosted" ;;
      pypi)   ask NEXUS_REPO_PYPI   "pypi hosted repository name"   "pypi-hosted" ;;
      docker) ask NEXUS_REPO_DOCKER "docker hosted repository name" "docker-hosted"
              ask_optional NEXUS_DOCKER_HOST "docker registry endpoint (host:port, blank = use $NEXUS_URL/repository/<repo>)" "${NEXUS_DOCKER_HOST:-}" ;;
    esac
  done
}

save_config() {
  local answer=y
  # Never write a password to disk without being asked to: a non-interactive
  # run is expected to get its credentials from the environment.
  if [ "$ASSUME_YES" = 1 ]; then
    return 0
  fi
  if [ "$ASSUME_YES" != 1 ]; then
    printf 'Save these settings (including the password) to %s? [Y/n]: ' "$CONFIG_FILE" >&2
    IFS= read -r answer || true
    answer=${answer:-y}
  fi
  case "$answer" in
    y|Y|yes|YES) ;;
    *) return 0 ;;
  esac
  local old_umask
  old_umask=$(umask)
  umask 077
  {
    printf '# Written by push.sh. Contains a password: keep mode 600.\n'
    printf 'NEXUS_URL=%s\n' "$(shq "$NEXUS_URL")"
    printf 'NEXUS_USER=%s\n' "$(shq "$NEXUS_USER")"
    printf 'NEXUS_PASS=%s\n' "$(shq "$NEXUS_PASS")"
    [ -n "${NEXUS_REPO_APT:-}" ]    && printf 'NEXUS_REPO_APT=%s\n' "$(shq "$NEXUS_REPO_APT")"
    [ -n "${NEXUS_REPO_NPM:-}" ]    && printf 'NEXUS_REPO_NPM=%s\n' "$(shq "$NEXUS_REPO_NPM")"
    [ -n "${NEXUS_REPO_PYPI:-}" ]   && printf 'NEXUS_REPO_PYPI=%s\n' "$(shq "$NEXUS_REPO_PYPI")"
    [ -n "${NEXUS_REPO_DOCKER:-}" ] && printf 'NEXUS_REPO_DOCKER=%s\n' "$(shq "$NEXUS_REPO_DOCKER")"
    [ -n "${NEXUS_DOCKER_HOST:-}" ] && printf 'NEXUS_DOCKER_HOST=%s\n' "$(shq "$NEXUS_DOCKER_HOST")"
    [ -n "${NEXUS_DOCKER_KEEP_REGISTRY:-}" ] && printf 'NEXUS_DOCKER_KEEP_REGISTRY=%s\n' "$(shq "$NEXUS_DOCKER_KEEP_REGISTRY")"
    true
  } > "$CONFIG_FILE"
  umask "$old_umask"
  chmod 600 "$CONFIG_FILE"
  info "saved $CONFIG_FILE (mode 600)"
}

# shq single-quotes a value for safe re-sourcing.
shq() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

# ---------------------------------------------------------------- push state

# State lives inside the bundle and is keyed by Nexus host plus repository, so
# the same mirror can be pushed to more than one Nexus and a repeated run only
# uploads what is genuinely new.
state_file() {
  local repo=$1 host
  host=$(printf '%s' "$NEXUS_URL" | sed -e 's#^[a-zA-Z]*://##' -e 's#[/:]#_#g')
  printf '%s/.push-state/%s-%s.tsv' "$BUNDLE" "$host" "$repo"
}

state_init() {
  mkdir -p "$BUNDLE/.push-state"
  local f
  f=$(state_file "$1")
  [ -f "$f" ] || : > "$f"
}

# state_status prints ok, fail or nothing for a digest.
state_status() {
  local repo=$1 key=$2 f
  f=$(state_file "$repo")
  [ -f "$f" ] || return 0
  awk -F'\t' -v k="$key" '$1==k {s=$3} END {if (s!="") print s}' "$f"
}

state_record() {
  local repo=$1 key=$2 status=$3 path=$4 f
  f=$(state_file "$repo")
  printf '%s\t%s\t%s\t%s\n' "$key" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$status" "$path" >> "$f"
}

# should_push decides whether an artifact still needs uploading.
#
# Only a recorded success is skipped. An artifact that failed last time is
# always tried again: a failure is usually caused by something on the server
# that has since been fixed, and silently skipping it would leave the mirror
# incomplete without saying so. --force additionally re-sends the successes.
should_push() {
  local repo=$1 key=$2 status
  [ "$FORCE" = 1 ] && return 0
  status=$(state_status "$repo" "$key")
  case "$status" in
    ok) return 1 ;;
    *)  return 0 ;;
  esac
}

# ---------------------------------------------------------------- counters

PUSHED=0; SKIPPED=0; FAILED=0

summary() {
  printf '\n%spushed %d, skipped %d, failed %d%s\n' "$C_BOLD" "$PUSHED" "$SKIPPED" "$FAILED" "$C_OFF"
  [ "$FAILED" -eq 0 ]
}

# ---------------------------------------------------------------- repositories
#
# Nexus answers an upload to a repository that does not exist with a bare
# 404 from its REST layer, once per artifact. Checking the names up front
# turns 273 identical failures into one clear question.

NEXUS_REPOS_CACHE=""

# nexus_fetch_repos caches the repository list in NEXUS_REPOS_CACHE.
#
# It deliberately runs in the main shell rather than inside a command
# substitution: die() only exits the subshell it runs in, so a fatal problem
# discovered here would otherwise be printed and then ignored.
NEXUS_REPO_LIST_CODE=""

nexus_fetch_repos() {
  if [ -n "$NEXUS_REPOS_CACHE" ] && [ -f "$NEXUS_REPOS_CACHE" ]; then
    return 0
  fi
  local body code
  body=$(mktemp)
  code=$(curl -sS -o "$body" -w '%{http_code}' -K "$CURL_CONF" \
    "$NEXUS_URL/service/rest/v1/repositories" </dev/null )
  NEXUS_REPO_LIST_CODE=$code
  if [ "$code" != "200" ]; then
    rm -f "$body"
    # A rejected login is not a reason to carry on quietly: every upload
    # that follows would fail the same way.
    case "$code" in
      401)
        die "Nexus rejected the credentials for user \"$NEXUS_USER\" (HTTP 401).
         Check the password, then re-run. To be asked for it again:
           push.sh --reconfigure      (or delete $CONFIG_FILE)" ;;
      429)
        die "Nexus answered HTTP 429: it is rate-limiting authentication for \"$NEXUS_USER\".
         Nexus does this after a few failed logins and logs \"retryAfter=30s\", so wait
         about half a minute and run push.sh again. If the password itself is wrong:
           push.sh --reconfigure" ;;
      000)
        die "cannot reach $NEXUS_URL (connection failed). Check the URL and that Nexus is running." ;;
    esac
    return 1
  fi
  NEXUS_REPOS_CACHE=$(mktemp)
  # Nexus pretty-prints its JSON; flatten it, split the array into one object
  # per line, then pull the three fields out of each.
  # "|| [ -n "$obj" ]" matters: the flattened JSON has no trailing newline, so
  # a plain read would silently drop the last repository in the list.
  tr -d ' \n' < "$body" | sed 's/},{/}\n{/g' | while IFS= read -r obj || [ -n "$obj" ]; do
    local n f t
    n=$(printf '%s' "$obj" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
    f=$(printf '%s' "$obj" | sed -n 's/.*"format":"\([^"]*\)".*/\1/p')
    t=$(printf '%s' "$obj" | sed -n 's/.*"type":"\([^"]*\)".*/\1/p')
    [ -n "$n" ] && printf '%s\t%s\t%s\n' "$n" "$f" "$t"
  done > "$NEXUS_REPOS_CACHE"
  rm -f "$body"
  return 0
}

# ensure_repo VARNAME FORMAT verifies that the configured repository exists
# and has the right format, offering the ones that do when it does not.
ensure_repo() {
  local var=$1 format=$2 want list line name fmt type candidates n choice
  eval "want=\${$var:-}"
  [ -n "$want" ] || return 0

  if ! nexus_fetch_repos; then
    warn "cannot list repositories on $NEXUS_URL (HTTP ${NEXUS_REPO_LIST_CODE:-?}); skipping the pre-flight check"
    return 0
  fi
  list=$(cat "$NEXUS_REPOS_CACHE")

  while IFS=$'\t' read -r name fmt type; do
    if [ "$name" = "$want" ]; then
      if [ "$fmt" != "$format" ]; then
        die "repository \"$want\" on $NEXUS_URL is a $fmt repository, but $format artifacts are being pushed"
      fi
      if [ "$type" = "proxy" ] || [ "$type" = "group" ]; then
        die "repository \"$want\" is a $type repository; artifacts can only be uploaded to a hosted one"
      fi
      return 0
    fi
  done <<< "$list"

  candidates=$(printf '%s\n' "$list" | awk -F'\t' -v f="$format" '$2==f && $3=="hosted" {print $1}')
  printf '\n'
  warn "repository \"$want\" does not exist on $NEXUS_URL"
  if [ -z "$candidates" ]; then
    die "no hosted $format repository exists there either; create one in Nexus first (Administration > Repository > Create repository > $format (hosted))"
  fi
  if [ "$ASSUME_YES" = 1 ]; then
    die "hosted $format repositories that do exist: $(printf '%s' "$candidates" | tr '\n' ' ')"
  fi
  info "  hosted $format repositories that do exist:"
  n=0
  while IFS= read -r name; do
    n=$((n + 1))
    printf '    %2d) %s\n' "$n" "$name"
  done <<< "$candidates"
  printf '     or type another name, or press Enter to abort\n'
  printf '  Repository: ' >&2
  IFS= read -r choice || true
  [ -n "$choice" ] || die "no repository chosen"
  if printf '%s' "$choice" | grep -qE '^[0-9]+$'; then
    choice=$(printf '%s\n' "$candidates" | sed -n "${choice}p")
    [ -n "$choice" ] || die "invalid selection"
  fi
  eval "$var=\$choice"
  info "  using $choice"
  REPO_CHANGED=1
}
