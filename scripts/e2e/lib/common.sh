# Shared helpers of the end-to-end scripts; source it, do not run it.
# Needs curl and jq. BASE defaults to the test environment; codes are read
# from the dev inbox and human verification passes with the environment's
# CAPTCHA_BYPASS_TOKEN (taken from .env when not exported).

BASE="${BASE:-https://astras.vip}"
BYPASS="${CAPTCHA_BYPASS_TOKEN:-$(grep '^CAPTCHA_BYPASS_TOKEN=' .env | cut -d= -f2- | tr -d '"')}"
RUN="$(date +%s)"
WORK="$(mktemp -d)"
AT_EXIT=()
# AT_END runs after the at_exit commands (lib/remote.sh closes its ssh
# connection there, which those commands may still need).
AT_END=""
trap 'for c in ${AT_EXIT[@]+"${AT_EXIT[@]}"}; do eval "$c" || true; done; [[ -z $AT_END ]] || eval "$AT_END" || true; rm -rf "$WORK"' EXIT
STATUS="" BODY="" TICKET=""

# at_exit COMMAND runs the COMMAND string when the script ends, also after
# a failed check: scripts use it to leave nothing in the shared order book.
at_exit() { AT_EXIT+=("$1"); }
APP=(-H 'X-Client-Type: APP')

# call METHOD PATH JSON [curl args...] sets STATUS and BODY.
call() {
  local method=$1 path=$2 data=$3
  shift 3
  local args=(-s -o "$WORK/body" -w '%{http_code}' -X "$method" "$BASE$path")
  if [[ -n "$data" ]]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  # A network failure (not an HTTP error) is reported, not silent: outside
  # a condition set -e then stops the script; inside eventually it retries.
  STATUS=$(curl "${args[@]}" "$@") || {
    printf 'curl %s %s failed (exit %s)\n' "$method" "$path" "$?" >&2
    return 1
  }
  BODY=$(cat "$WORK/body")
}

# expect STATUS CODE WHAT checks the last call; CODE is "-" for success.
expect() {
  local code
  code=$(jq -r '.code // "-"' <<<"$BODY" 2>/dev/null || true)
  code=${code:--}
  if [[ "$STATUS" != "$1" || "$code" != "$2" ]]; then
    printf 'FAIL %s: got %s %s, want %s %s\n%s\n' "$3" "$STATUS" "$code" "$1" "$2" "$BODY" >&2
    exit 1
  fi
  printf 'ok   %s\n' "$3"
}

# check CONDITION WHAT fails unless the jq CONDITION holds for BODY.
check() {
  if [[ $(jq -r "$1" <<<"$BODY") != true ]]; then
    printf 'FAIL %s: %s does not hold for\n%s\n' "$2" "$1" "$BODY" >&2
    exit 1
  fi
  printf 'ok   %s\n' "$2"
}

inbox_count() {
  curl -s "$BASE/v1/dev/messages?target=$(jq -rn --arg e "$1" '$e|@uri')&limit=50" | jq '.messages | length'
}

# otp SCENE EMAIL DEVICE [ACCESS_TOKEN] requests a code by email, reads it
# from the dev inbox and sets TICKET.
otp() { otp_via EMAIL "$@"; }

# otp_via CHANNEL SCENE TARGET DEVICE [ACCESS_TOKEN] does the same over
# EMAIL or SMS. TARGET is where the code arrives (for STEP_UP the bound
# address of the channel; the request itself names no identifier then).
# SMS through the mock provider needs the auth.sms flag.
otp_via() {
  local channel=$1 scene=$2 target=$3 device=$4 token=${5:-} before inbox code challenge
  local auth=()
  [[ -n "$token" ]] && auth=(-H "Authorization: Bearer $token")
  before=$(inbox_count "$target")
  call POST /v1/auth/otp/request "{\"scene\":\"$scene\",\"channel\":\"$channel\",\"identifier\":\"$target\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$device\"}" ${auth[@]+"${auth[@]}"}
  expect 200 - "otp/request $scene by $channel"
  challenge=$(jq -r .challenge_id <<<"$BODY")
  for _ in $(seq 20); do
    inbox=$(curl -s "$BASE/v1/dev/messages?target=$(jq -rn --arg e "$target" '$e|@uri')&limit=50")
    if (( $(jq '.messages | length' <<<"$inbox") > before )); then
      break
    fi
    sleep 0.5
  done
  # Mails carry the code in the subject; SMS have only a body.
  code=$(jq -r '.messages[0] | .subject + " " + .body' <<<"$inbox" | grep -oE '[0-9]{6}' | head -1)
  call POST /v1/auth/otp/verify "{\"challenge_id\":\"$challenge\",\"code\":\"$code\",\"device_id\":\"$device\"}"
  expect 200 - "otp/verify $scene"
  TICKET=$(jq -r .otp_ticket <<<"$BODY")
  date +%s >"$(code_stamp "$target")"
}

# code_stamp TARGET is the file holding when TARGET last got a code (bash
# 3.2 on macOS has no associative arrays).
code_stamp() { printf '%s/code-%s' "$WORK" "$(tr -c 'A-Za-z0-9' '_' <<<"$1")"; }

# wait_resend TARGET waits out the 60-second resend window of TARGET after
# its last code (§5.3).
wait_resend() {
  local stamp last now
  stamp=$(code_stamp "$1")
  last=$(cat "$stamp" 2>/dev/null || echo 0)
  now=$(date +%s)
  if (( last + 62 > now )); then
    sleep $(( last + 62 - now ))
  fi
}

# register EMAIL DEVICE PASSWORD signs a new APP user up and sets BODY to
# the token response.
register() { # register EMAIL DEVICE PASSWORD [COUNTRY, default SG]
  local email=$1 device=$2 password=$3 country=${4:-SG} terms risk
  call GET /v1/auth/terms ""
  expect 200 - "terms"
  terms=$(jq -r .terms_version <<<"$BODY")
  risk=$(jq -r .risk_disclosure_version <<<"$BODY")
  otp REGISTER "$email" "$device"
  call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"$password\",\"country\":\"$country\",\"terms_version\":\"$terms\",\"risk_disclosure_version\":\"$risk\",\"device_id\":\"$device\"}" "${APP[@]}"
  expect 201 - "register"
}

# eventually TRIES WHAT CMD... reruns CMD every half second until it
# succeeds.
eventually() {
  local tries=$1 what=$2
  shift 2
  for _ in $(seq "$tries"); do
    if "$@" >/dev/null 2>&1; then
      printf 'ok   %s\n' "$what"
      return 0
    fi
    sleep 0.5
  done
  printf 'FAIL %s\n' "$what" >&2
  exit 1
}
