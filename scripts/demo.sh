#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
API_KEY="${API_KEY:-dev-api-key}"

cleanup() {
  docker compose -f "$COMPOSE_FILE" down >/dev/null
}

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"

  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H 'content-type: application/json' \
      -H "X-API-Key: $API_KEY" \
      -d "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "X-API-Key: $API_KEY"
  fi
}

json_field() {
  python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"
}

wait_for_api() {
  for _ in {1..60}; do
    if curl -fsS "$BASE_URL/readyz" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done

  echo "API did not become ready" >&2
  docker compose -f "$COMPOSE_FILE" logs --no-color custody-api >&2
  exit 1
}

trap cleanup EXIT

echo "Starting custody demo stack..."
docker compose -f "$COMPOSE_FILE" up --build -d
wait_for_api

echo "Running EVM quorum flow..."
evm_wallet_json="$(request POST /v1/wallets '{"chain":"evm"}')"
evm_wallet_id="$(printf '%s' "$evm_wallet_json" | json_field id)"
evm_address="$(printf '%s' "$evm_wallet_json" | json_field address)"
echo "EVM wallet: $evm_wallet_id $evm_address"

evm_txn_json="$(request POST /v1/transactions "$(cat <<JSON
{
  "wallet_id": "$evm_wallet_id",
  "to": "0x1111111111111111111111111111111111111111",
  "amount": "1000000000000000"
}
JSON
)")"
evm_txn_id="$(printf '%s' "$evm_txn_json" | json_field id)"
request POST "/v1/transactions/$evm_txn_id/cosign" '{"signer_id":"alice"}' >/dev/null
request POST "/v1/transactions/$evm_txn_id/cosign" '{"signer_id":"bob"}' >/dev/null
evm_broadcast_json="$(request POST "/v1/transactions/$evm_txn_id/broadcast")"
evm_hash="$(printf '%s' "$evm_broadcast_json" | json_field broadcast_hash)"
echo "EVM broadcast: $evm_hash"

echo "Running Bitcoin quorum flow..."
btc_wallet_json="$(request POST /v1/wallets '{"chain":"bitcoin"}')"
btc_wallet_id="$(printf '%s' "$btc_wallet_json" | json_field id)"
echo "Bitcoin wallet: $btc_wallet_id"

btc_txn_json="$(request POST /v1/transactions "$(cat <<JSON
{
  "wallet_id": "$btc_wallet_id",
  "to": "tb1qrecipient",
  "amount": "50000",
  "fee_rate_sats": 5,
  "utxos": [
    {
      "tx_id": "0000000000000000000000000000000000000000000000000000000000000000",
      "vout": 0,
      "amount_sats": 75000,
      "script_pub_key": "0014deadbeef"
    }
  ]
}
JSON
)")"
btc_txn_id="$(printf '%s' "$btc_txn_json" | json_field id)"
request POST "/v1/transactions/$btc_txn_id/cosign" '{"signer_id":"alice"}' >/dev/null
request POST "/v1/transactions/$btc_txn_id/cosign" '{"signer_id":"bob"}' >/dev/null
btc_broadcast_json="$(request POST "/v1/transactions/$btc_txn_id/broadcast")"
btc_hash="$(printf '%s' "$btc_broadcast_json" | json_field broadcast_hash)"
echo "Bitcoin broadcast: $btc_hash"

echo "Checking persistence..."
docker compose -f "$COMPOSE_FILE" exec -T postgres \
  psql -U custody -d custody -c "select count(*) as wallets from wallets; select count(*) as proposals from transaction_proposals; select count(*) as audit_events from audit_events;"

echo "Checking Bitcoin regtest node..."
docker compose -f "$COMPOSE_FILE" exec -T bitcoind \
  bitcoin-cli -regtest -rpcuser=custody -rpcpassword=custody getblockchaininfo >/dev/null

echo "Checking metrics..."
curl -fsS "$BASE_URL/metrics" | grep -q 'custody_transactions_broadcast_total'

echo "Checking audit API..."
request GET /v1/audit/events | grep -q 'transaction.broadcast'

echo "Demo completed successfully."
