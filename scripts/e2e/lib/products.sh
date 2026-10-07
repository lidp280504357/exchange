# Contract product lines for the end-to-end scripts (design 2026-10-07,
# product switches, K1b); source it after common.sh and remote.sh.
#
#   product_close NAME   closes the line (usdt_m or coin_m) as the console
#                        does: product.NAME off, then derivatives-service's
#                        cancel-open, whose answer it leaves in BODY
#   product_open NAME    opens it again
#   product_state NAME   derivatives-service's view of the line in BODY:
#                        {product, closed, open_orders, open_positions}
#
# A line closed here is opened again when the script ends, whatever
# happens; one that cannot be is reported with the command that does it by
# hand. Closing a line cancels every user's open orders on its contracts
# (the market makers' stay): on the test server that is the price of the
# seconds a script keeps it closed. Services read the flags every 5
# seconds (flags.RefreshInterval); cancel-open reads them at once.

PRODUCT_CLOSED=""

# deriv GET|POST PATH [BODY] calls derivatives-service's internal API in
# its container (busybox wget: a 2xx answer prints its body, any other
# fails).
deriv() {
  local post=""
  if [[ $1 == POST ]]; then
    post="--header 'Content-Type: application/json' --post-data $(printf %q "$3")"
  fi
  remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service wget -qO- $post 'http://127.0.0.1:8095$2'"
}

product_close() {
  local name=$1 was
  was=$(exchangectl flags show "product.$name" | jq -r .enabled)
  [[ $was == true ]] || { echo "FAIL product.$name is not open before the run ($was)" >&2; exit 1; }
  PRODUCT_CLOSED=$name
  exchangectl flags set "product.$name" --off --reason "e2e $(basename "$0"): the $name line closed for a moment" >/dev/null
  BODY=$(deriv POST "/internal/products/$name/cancel-open" "{\"actor\":\"e2e-ops\",\"reason\":\"e2e $(basename "$0"): the $name line closed\"}")
}

product_open() {
  exchangectl flags set "product.$1" --on --reason "e2e $(basename "$0"): the $1 line open again" >/dev/null
  PRODUCT_CLOSED=""
}

product_state() {
  BODY=$(deriv GET "/internal/products/$1")
}

# product_back opens a line still closed by this script, once.
product_back() {
  [[ -n $PRODUCT_CLOSED ]] || return 0
  if exchangectl flags set "product.$PRODUCT_CLOSED" --on --reason "e2e $(basename "$0"): the line open again as the run ends" >/dev/null; then
    PRODUCT_CLOSED=""
    return 0
  fi
  echo "FAIL product.$PRODUCT_CLOSED not opened again; by hand: exchangectl flags set product.$PRODUCT_CLOSED --on --reason ..." >&2
  EXIT_FAILED=1
}
at_exit product_back
