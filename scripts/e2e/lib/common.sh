# Shared helpers of the end-to-end scripts; source it, do not run it.
# Needs curl and jq. BASE defaults to the test environment; codes are read
# from the dev inbox and human verification passes with the environment's
# CAPTCHA_BYPASS_TOKEN (taken from .env when not exported).

BASE="${BASE:-https://astras.vip}"
BYPASS="${CAPTCHA_BYPASS_TOKEN:-$(grep '^CAPTCHA_BYPASS_TOKEN=' .env | cut -d= -f2- | tr -d '"')}"
RUN="$(date +%s)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STATUS="" BODY="" TICKET=""
APP=(-H 'X-Client-Type: APP')

# call METHOD PATH JSON [curl args...] sets STATUS and BODY.
call() {
  local method=$1 path=$2 data=$3
  shift 3
  local args=(-s -o "$WORK/body" -w '%{http_code}' -X "$method" "$BASE$path")
  if [[ -n "$data" ]]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  STATUS=$(curl "${args[@]}" "$@")
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

# otp SCENE EMAIL DEVICE [ACCESS_TOKEN] requests a code, reads it from the
# dev inbox and sets TICKET.
otp() {
  local scene=$1 email=$2 device=$3 token=${4:-} before inbox code challenge
  local auth=()
  [[ -n "$token" ]] && auth=(-H "Authorization: Bearer $token")
  before=$(inbox_count "$email")
  call POST /v1/auth/otp/request "{\"scene\":\"$scene\",\"channel\":\"EMAIL\",\"identifier\":\"$email\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$device\"}" ${auth[@]+"${auth[@]}"}
  expect 200 - "otp/request $scene"
  challenge=$(jq -r .challenge_id <<<"$BODY")
  for _ in $(seq 20); do
    inbox=$(curl -s "$BASE/v1/dev/messages?target=$(jq -rn --arg e "$email" '$e|@uri')&limit=50")
    if (( $(jq '.messages | length' <<<"$inbox") > before )); then
      break
    fi
    sleep 0.5
  done
  code=$(jq -r '.messages[0].subject' <<<"$inbox" | grep -oE '[0-9]{6}')
  call POST /v1/auth/otp/verify "{\"challenge_id\":\"$challenge\",\"code\":\"$code\",\"device_id\":\"$device\"}"
  expect 200 - "otp/verify $scene"
  TICKET=$(jq -r .otp_ticket <<<"$BODY")
}

# register EMAIL DEVICE PASSWORD signs a new APP user up and sets BODY to
# the token response.
register() {
  local email=$1 device=$2 password=$3 terms risk
  call GET /v1/auth/terms ""
  expect 200 - "terms"
  terms=$(jq -r .terms_version <<<"$BODY")
  risk=$(jq -r .risk_disclosure_version <<<"$BODY")
  otp REGISTER "$email" "$device"
  call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"$password\",\"country\":\"SG\",\"terms_version\":\"$terms\",\"risk_disclosure_version\":\"$risk\",\"device_id\":\"$device\"}" "${APP[@]}"
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
