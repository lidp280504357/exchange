# held_fees N leaves N custodian's fees held for a person on the stand-in
# custodian UDUNMOCK (ADR-0017) and sets HELD_FEES to their withdrawals'
# IDs, for a test of the console's book and write-off: a fresh account of
# region AQ (eligible for TEST_ASSETS) with an authenticator app, TUSD
# from the stand-in, a TRON-TEST payee of its own (a minute's cool-off),
# N withdrawals of 10 TUSD an operator approves, which the stand-in sends
# reporting a fee of 999 TUSD and taking none: above the bound (the lesser
# of the 10 sent and 5 times the network's fee of 5), each fee is held,
# and no balance moves, so the reconciliation finds no shortfall (a fee
# then booked leaves a small surplus at the stand-in, never a shortfall).
# Source it after lib/common.sh and lib/remote.sh; about two minutes. It
# fails at once unless wallet-service's UDUNMOCK custodian is the stand-in
# udun-mock (held_standin tells first, for a caller that skips instead).
#
#   source "$(dirname "$0")/lib/held-fees.sh"
#   held_standin || { echo "SKIP: UDUNMOCK is not the stand-in"; exit 0; }
#   held_fees 2
#   echo "${HELD_FEES[@]}"

# HELD_PAYEE is the held fees' payee, no other script's: the stand-in's
# outcome for an address holds for everyone sending to it.
HELD_PAYEE=TT8ZMouf3kkdTWh61n4WNTdR3QntPoj4Kv
HELD_COIN="195:TQQCuyVcUEknTGyfSRKhcUuLZfEe93qWpy"

# held_standin reports whether wallet-service's UDUNMOCK custodian is the
# stand-in udun-mock (review AX: never a real gateway).
held_standin() {
  [[ $(remote "sudo docker compose $COMPOSE_FILES exec -T wallet-service printenv UDUNMOCK_GATEWAY_URL </dev/null || true") == http://udun-mock:* ]]
}

held_mock() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T udun-mock /app/udun-mock $args"
}

held_fees() {
  local n=$1 email device token secret last step addr id i
  local -a auth
  HELD_FEES=()
  held_standin || { echo "FAIL held_fees: wallet-service's UDUNMOCK custodian is not the stand-in udun-mock" >&2; exit 1; }
  email="e2e-held-fees-$RUN@example.com"
  device="e2e-held-fees-$RUN"
  echo "== $n custodian's fees held for a person ($email)"
  register "$email" "$device" "e2e held fees $RUN" AQ
  token=$(jq -r .access_token <<<"$BODY")
  auth=(-H "Authorization: Bearer $token")

  wait_resend "$email"
  otp STEP_UP "$email" "$device" "$token"
  call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$device\"}" "${auth[@]}"
  expect 200 - "step-up by mail"
  call POST /v1/auth/totp/setup "" "${auth[@]}" -H "X-Step-Up-Token: $(jq -r .step_up_token <<<"$BODY")"
  expect 200 - "authenticator setup"
  secret=$(jq -r .secret <<<"$BODY")
  last=$(($(date +%s) / 30))
  call POST /v1/auth/totp/confirm "{\"code\":\"$(node "$(dirname "${BASH_SOURCE[0]}")/totp.mjs" "$secret")\"}" "${auth[@]}"
  expect 204 - "bound"
  held_step_up() {
    while (($(date +%s) / 30 + 1 <= last)); do sleep 1; done
    last=$(($(date +%s) / 30 + 1))
    call POST /v1/auth/step-up "{\"totp_code\":\"$(node "$(dirname "${BASH_SOURCE[0]}")/totp.mjs" "$secret" 1)\",\"device_id\":\"$device\"}" "${auth[@]}"
    expect 200 - "step-up by the app"
    step=$(jq -r .step_up_token <<<"$BODY")
  }

  call GET "/v1/wallet/deposit-address?asset=TUSD&network=TRON-TEST" "" "${auth[@]}"
  expect 200 - "a TRON-TEST address"
  addr=$(jq -r .address <<<"$BODY")
  held_mock deposit --address "$addr" --coin "$HELD_COIN" --amount $((15 * n + 5)) >/dev/null
  eventually 180 "the stand-in's TUSD credited" held_credited
  exchangectl wallet custody-fee-unit --provider UDUNMOCK --asset TUSD --network TRON-TEST --unit SELF \
    --reason "end-to-end: the mock gateway charges in the coin" >/dev/null
  held_mock outcome --address "$HELD_PAYEE" --status 3 --fee 999000000 --charge 0 >/dev/null
  at_exit "held_mock outcome --address $HELD_PAYEE --status 3 >/dev/null 2>&1"
  held_step_up
  call POST /v1/wallet/withdraw-addresses "{\"network\":\"TRON-TEST\",\"address\":\"$HELD_PAYEE\"}" "${auth[@]}" -H "X-Step-Up-Token: $step"
  expect 201 - "the payee added"
  sleep 62 # a new address cools off for a minute on the test server

  for ((i = 0; i < n; i++)); do
    held_step_up
    call POST /v1/wallet/withdrawals "{\"asset\":\"TUSD\",\"network\":\"TRON-TEST\",\"address\":\"$HELD_PAYEE\",\"amount\":\"10\"}" \
      "${auth[@]}" -H "X-Step-Up-Token: $step"
    expect 201 - "10 TUSD requested"
    id=$(jq -r .id <<<"$BODY")
    if [[ $(jq -r .status <<<"$BODY") == PENDING_REVIEW ]]; then
      exchangectl wallet approve "$id" --reviewer e2e-ops --reason "end-to-end: a fee to hold" >/dev/null
    fi
    HELD_FEES+=("$id")
  done
  for id in "${HELD_FEES[@]}"; do
    HELD_ID=$id
    eventually 240 "withdrawal $id sent, its fee of 999 TUSD held" held_fee_held
  done
}

# held_credited reports whether the held fees' account got its TUSD.
held_credited() {
  call GET /v1/wallet/deposits "" "${auth[@]}" >/dev/null &&
    [[ $(jq '[.items[] | select(.status == "CREDITED" and .network == "TRON-TEST")] | length' <<<"$BODY") == 1 ]]
}

# held_fee_held reports whether withdrawal HELD_ID's fee is held.
held_fee_held() {
  [[ $(pg "SELECT status FROM wallet.chain_fees WHERE reference = '$HELD_ID'") == HELD ]]
}
