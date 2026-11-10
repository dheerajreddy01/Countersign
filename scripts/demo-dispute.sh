#!/usr/bin/env bash
# Inject a sample PayPal dispute webhook into the gateway, to drive the demo
# without opening a real sandbox dispute.
#
# The gateway must be running with GATEWAY_SKIP_SIGNATURE_VERIFY=true: this
# payload is not PayPal-signed, and that flag is local-development only. On a
# deployed gateway (signature verification on) a real sandbox dispute is the only
# way in, by design — see docs/deploy.md.
#
# Usage:
#   scripts/demo-dispute.sh [ORDER_ID] [PAYPAL_REASON] [AMOUNT]
#
# ORDER_ID should match the order a mandate was recorded against (cmd/demoagent),
# so the mandate matches the disputed transaction. PAYPAL_REASON is a PayPal enum:
#   MERCHANDISE_OR_SERVICE_NOT_RECEIVED   -> item_not_received (default)
#   UNAUTHORISED                          -> unauthorized
#   MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED -> not_as_described
set -euo pipefail

GATEWAY="${GATEWAY_URL:-http://localhost:8080}"
ORDER_ID="${1:-7YH29301}"
REASON="${2:-MERCHANDISE_OR_SERVICE_NOT_RECEIVED}"
AMOUNT="${3:-149.00}"
DISPUTE_ID="PP-D-$(date +%s)"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
DUE="$(date -u -v+7d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+7 days' +%Y-%m-%dT%H:%M:%SZ)"

BODY=$(cat <<JSON
{
  "id": "WH-${DISPUTE_ID}",
  "event_type": "CUSTOMER.DISPUTE.CREATED",
  "create_time": "${NOW}",
  "resource": {
    "dispute_id": "${DISPUTE_ID}",
    "reason": "${REASON}",
    "status": "WAITING_FOR_SELLER_RESPONSE",
    "dispute_amount": { "currency_code": "USD", "value": "${AMOUNT}" },
    "dispute_life_cycle_stage": "CHARGEBACK",
    "seller_response_due_date": "${DUE}",
    "create_time": "${NOW}",
    "disputed_transactions": [
      {
        "seller_transaction_id": "${ORDER_ID}",
        "invoice_id": "INV-${ORDER_ID}",
        "item_name": "Trail Runner GTX",
        "buyer": { "name": "Jordan Rivera", "email": "buyer@example.com" }
      }
    ]
  }
}
JSON
)

echo "Posting CUSTOMER.DISPUTE.CREATED  dispute=${DISPUTE_ID}  order=${ORDER_ID}  reason=${REASON}  amount=${AMOUNT}"
curl -fsS -X POST "${GATEWAY}/webhooks/paypal" \
  -H 'Content-Type: application/json' \
  -H 'PAYPAL-TRANSMISSION-ID: demo-injection' \
  --data "${BODY}"
echo
echo "Watch it flow: gateway -> normalizer -> case service -> evidence builder (scores + drafts)."
echo "Then open the console at http://localhost:3000 and find the new case."
