# Shared helpers of the end-to-end scripts; source it, do not run it.
# Needs curl and jq. BASE defaults to the test environment; codes are read
# from the dev inbox and human verification passes with the environment's
# CAPTCHA_BYPASS_TOKEN (taken from .env when not exported).

BASE="${BASE:-https://astras.vip}"
BYPASS="${CAPTCHA_BYPASS_TOKEN:-$(grep '^CAPTCHA_BYPASS_TOKEN=' .env | cut -d= -f2- | tr -d '"')}"
RUN="$(date +%s)"
WORK="$(mktemp -d)"
# Every curl of the scripts over HTTP/1.1 (curl reads $CURL_HOME/.curlrc,
# not ~/.curlrc): the Mac's curl (8.7.1) fails now and then on HTTP/2 with
# a framing error, exit 16, on answers the server sent - admin.sh three
# times on 2026-10-07 (it set this first), custody.sh's raw POST of a
# forged callback on 2026-10-10, which set -e ended without a word.
printf '%s\n' '--http1.1' >"$WORK/.curlrc"
export CURL_HOME=$WORK
AT_EXIT=()
# AT_END runs after the at_exit commands (lib/remote.sh closes its ssh
# connection there, which those commands may still need). An at_exit
# command sets EXIT_FAILED to fail the run even when the script itself got
# through (web.sh: a switch it could not put back).
AT_END=""
EXIT_FAILED=""
run_at_exit() {
  local i
  for ((i = ${#AT_EXIT[@]} - 1; i >= 0; i--)); do
    eval "${AT_EXIT[$i]}" || true
  done
  [[ -z $AT_END ]] || eval "$AT_END" || true
  rm -rf "$WORK"
  [[ -z $EXIT_FAILED ]] || exit 1
}
trap run_at_exit EXIT
STATUS="" BODY="" TICKET=""

# at_exit COMMAND runs the COMMAND string when the script ends, also after
# a failed check: scripts use it to leave nothing in the shared order book.
# Like defer, the last one registered runs first.
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
  # A failure to connect (curl 6, 7, 28 with the connect timeout, 35 in the
  # TLS handshake) happens before the request is sent, so it is retried:
  # this machine's path to Cloudflare drops a connection now and then, at
  # times for half a minute (2026-10-02), hence six tries over about 35 s.
  # Any other network failure is reported, not silent: outside a condition
  # set -e then stops the script; inside eventually it retries.
  local attempt rc=0
  for attempt in 1 2 3 4 5 6; do
    if STATUS=$(curl --connect-timeout 15 "${args[@]}" "$@"); then
      BODY=$(cat "$WORK/body")
      return 0
    else
      rc=$?
    fi
    case $rc in
      6 | 7 | 28 | 35) printf 'curl %s %s: no connection (exit %s, attempt %s)\n' "$method" "$path" "$rc" "$attempt" >&2 ;;
      *) break ;;
    esac
    sleep $((attempt < 3 ? 2 : 8))
  done
  printf 'curl %s %s failed (exit %s)\n' "$method" "$path" "$rc" >&2
  return 1
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
  call GET "/v1/dev/messages?target=$(jq -rn --arg e "$1" '$e|@uri')&limit=50" "" && jq '.messages | length' <<<"$BODY"
}

# otp SCENE EMAIL DEVICE [ACCESS_TOKEN] requests a code by email, reads it
# from the dev inbox and sets TICKET.
otp() { otp_via EMAIL "$@"; }

# otp_via CHANNEL SCENE TARGET DEVICE [ACCESS_TOKEN] does the same over
# EMAIL or SMS. TARGET is where the code arrives (for STEP_UP the bound
# address of the channel; the request itself names no identifier then).
# SMS through the mock provider needs the auth.sms flag.
otp_via() {
  local channel=$1 scene=$2 target=$3 device=$4 token=${5:-} before code challenge
  local auth=()
  [[ -n "$token" ]] && auth=(-H "Authorization: Bearer $token")
  before=$(inbox_count "$target")
  call POST /v1/auth/otp/request "{\"scene\":\"$scene\",\"channel\":\"$channel\",\"identifier\":\"$target\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$device\"}" ${auth[@]+"${auth[@]}"}
  expect 200 - "otp/request $scene by $channel"
  challenge=$(jq -r .challenge_id <<<"$BODY")
  code=$(await_code "$target" "$before")
  call POST /v1/auth/otp/verify "{\"challenge_id\":\"$challenge\",\"code\":\"$code\",\"device_id\":\"$device\"}"
  expect 200 - "otp/verify $scene"
  TICKET=$(jq -r .otp_ticket <<<"$BODY")
  date +%s >"$(code_stamp "$target")"
}

# await_code TARGET BEFORE waits for the dev inbox of TARGET to hold more
# than BEFORE messages and prints the code of the newest.
await_code() {
  local target=$1 before=$2 inbox=""
  for _ in $(seq 20); do
    if call GET "/v1/dev/messages?target=$(jq -rn --arg e "$target" '$e|@uri')&limit=50" ""; then
      inbox=$BODY
      if (($(jq '.messages | length' <<<"$inbox") > before)); then
        break
      fi
    fi
    sleep 0.5
  done
  # Mails carry the code in the subject; SMS have only a body.
  jq -r '.messages[0] | .subject + " " + .body' <<<"$inbox" | grep -oE '[0-9]{6}' | head -1
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
# the token response, and notes the account in $E2E_REGISTERED: this run's
# accounts are marked TEST and cleared out when the script ends
# (mark_test_accounts).
register() { # register EMAIL DEVICE PASSWORD [COUNTRY, default SG]
  local email=$1 device=$2 password=$3 country=${4:-SG} terms risk
  call GET /v1/auth/terms ""
  expect 200 - "terms"
  terms=$(jq -r .terms_version <<<"$BODY")
  risk=$(jq -r .risk_disclosure_version <<<"$BODY")
  otp REGISTER "$email" "$device"
  call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"$password\",\"country\":\"$country\",\"terms_version\":\"$terms\",\"risk_disclosure_version\":\"$risk\",\"device_id\":\"$device\"}" "${APP[@]}"
  expect 201 - "register"
  jq -r .user_id <<<"$BODY" >>"$E2E_REGISTERED"
}

# E2E_REGISTERED lists the accounts this run signed up, an ID a line:
# register adds its own, and a program the script runs that signs accounts
# up itself (the browser smokes) adds its ones to the same file - it is
# exported - so that the exit hook clears them out too.
export E2E_REGISTERED="$WORK/registered"
: >"$E2E_REGISTERED"

# mark_test_accounts marks the accounts this run registered TEST (L0: the
# console leaves test accounts out of its lists by default), in one call
# by their emails (e2e-...-$RUN@example.com, the fault drills'
# fault-...-$RUN@example.com) through exchangectl in a container on the
# test server, then clears out the ones in $E2E_REGISTERED (L4:
# exchangectl users purge by their IDs, not by the emails' second-long
# RUN, which a script of another session may share - B184 ④; the
# accounts' balances go to ADJUSTMENT, the accounts are closed and hidden,
# contract positions flattened and margin debts settled first; one they
# leave, one with a withdrawal in flight and one exempt from the purge,
# funding.sh's standing hedges, are left). Registered when this file is
# sourced, it is the last
# at_exit to run, after the script's own clean-ups. It marks when an
# account was signed up here or the script names its accounts for the run
# some other way (E2E_MARK_RUN=1: web.sh's and webflows.sh's browser
# scripts), and clears out only the ones listed (B187). A failure only
# warns.
E2E_MARK_RUN=""
# Set for scripts that armed the hook themselves before B187: they see it
# armed.
MARKS_TEST_ACCOUNTS=1
mark_test_accounts() {
  local out name ctl ids
  ids=$(sort -u "$E2E_REGISTERED" 2>/dev/null | grep -E '^[0-9a-f-]{36}$' | paste -sd, - || true)
  [[ -n $ids || -n $E2E_MARK_RUN ]] || return 0
  name=$(basename "$0" .sh)
  ctl="cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T -e EXCHANGECTL_ACTOR=e2e-$name user-service /app/exchangectl"
  if ! out=$(ssh -o ConnectTimeout=20 exchange "$ctl users kind --email-like '%-$RUN@example.com' --kind TEST --reason 'e2e $name'" 2>&1 </dev/null); then
    echo "warn: this run's accounts were not marked TEST: $(tail -1 <<<"$out")" >&2
    return 0
  fi
  [[ -n $ids ]] || return 0
  if ! out=$(ssh -o ConnectTimeout=20 exchange "$ctl users purge --user '$ids' --pace 0s --reason 'e2e $name done'" 2>&1 </dev/null); then
    echo "warn: this run's accounts were not all cleared out: $(grep -v '^skip' <<<"$out" | tail -1)" >&2
  fi
  grep '^skip\|^purged' <<<"$out" | sed 's/^/note: /' >&2 || true
}
at_exit mark_test_accounts

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
