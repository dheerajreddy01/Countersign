#!/usr/bin/env bash
# Seed a realistic spread of disputes through the real pipeline, for demos and
# for looking at the console with something other than one case repeated.
#
# scripts/demo-dispute.sh posts one dispute, always for the same buyer, product
# and respond-by date; post it twenty times and the board shows twenty identical
# cards in one column. This posts a varied set instead — different buyers,
# products, amounts and reasons, deadlines from overdue to weeks out, and a
# signed mandate on some orders (one deliberately under-authorised) — so every
# column of the deadline board and both recommendations have something in them.
#
# Each dispute goes through the gateway, so it is normalised, cased, evidenced,
# scored and drafted exactly as a real one is. Like demo-dispute.sh it needs the
# gateway running with GATEWAY_SKIP_SIGNATURE_VERIFY=true (local only): these
# payloads are not PayPal-signed.
#
# Usage:
#   scripts/seed-disputes.sh                 # gateway :8080, mandate service :8083
#   GATEWAY_URL=... MANDATE_SERVICE_URL=... scripts/seed-disputes.sh
#
# Run it again for another batch: ids are suffixed with the run time.
set -euo pipefail

GATEWAY="${GATEWAY_URL:-http://localhost:8080}"
MANDATES="${MANDATE_SERVICE_URL:-http://localhost:8083}"
RUN="$(date +%s)"

iso_in_hours() {
  # GNU date, then BSD date (macOS).
  date -u -d "@$(( $(date +%s) + $1 * 3600 ))" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -r "$(( $(date +%s) + $1 * 3600 ))" +%Y-%m-%dT%H:%M:%SZ
}

mandate() { # order amount_authorized instruction
  # Redirected by the shell, not `curl -o /dev/null`: a native Windows curl run
  # from Git Bash with MSYS_NO_PATHCONV=1 receives "/dev/null" literally.
  curl -fsS -X POST "$MANDATES/mandates" -H 'Content-Type: application/json' -d @- >/dev/null <<JSON
{"user":"user_$RANDOM","agent":"countersign-demo-agent/1","instruction":"$3",
 "constraints":["max_price=$2"],"amount_authorized":"$2","currency":"USD",
 "order_id":"$1","issued_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
JSON
}

dispute() { # n buyer email product amount reason due_in_hours
  local id="PP-D-$RUN-$1" order="ORD-$RUN-$1"
  local code
  code=$(curl -s -w '
%{http_code}' -X POST "$GATEWAY/webhooks/paypal" \
    -H 'Content-Type: application/json' -H 'PAYPAL-TRANSMISSION-ID: seed' -d @- <<JSON
{"id":"WH-$id","event_type":"CUSTOMER.DISPUTE.CREATED","create_time":"$(iso_in_hours 0)",
 "resource":{"dispute_id":"$id","reason":"$6","status":"WAITING_FOR_SELLER_RESPONSE",
  "dispute_amount":{"currency_code":"USD","value":"$5"},"dispute_life_cycle_stage":"CHARGEBACK",
  "seller_response_due_date":"$(iso_in_hours "$7")","create_time":"$(iso_in_hours 0)",
  "disputed_transactions":[{"seller_transaction_id":"$order","invoice_id":"INV-$order",
   "item_name":"$4","buyer":{"name":"$2","email":"$3"}}]}}
JSON
  )
  code=$(printf '%s' "$code" | tail -n1)
  printf '  %-4s %-18s %-26s %9s  %-38s due %+4dh  -> %s\n' "$1" "$2" "$4" "\$$5" "$6" "$7" "$code"
  [ "$code" = 200 ] || { echo "gateway refused it (is GATEWAY_SKIP_SIGNATURE_VERIFY=true?)" >&2; exit 1; }
}

N=MERCHANDISE_OR_SERVICE_NOT_RECEIVED
U=UNAUTHORISED
A=MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED

echo "Recording mandates (the agent's signed authorisations) for some orders..."
mandate "ORD-$RUN-01" 250.00 "buy the Aurora Pro headphones, under 250"
mandate "ORD-$RUN-04" 900.00 "order the standing desk frame, budget 900"
mandate "ORD-$RUN-07" 60.00  "reorder the espresso beans, 2kg, under 60"
mandate "ORD-$RUN-09" 300.00 "buy a 4K monitor, under 300"   # less than was charged: will not count

echo "Posting disputes through $GATEWAY ..."
#        n   buyer               email                    product                    amount  reason due(h)
dispute 01 "Maya Chen"          maya.chen@example.com    "Aurora Pro Headphones"    229.00  $N   -6
dispute 02 "Daniel Okafor"      d.okafor@example.com     "Trail Runner GTX"         149.00  $A    5
dispute 03 "Priya Raman"        priya.r@example.com      "Linen Duvet Set"          118.50  $N   20
dispute 04 "Lucas Moreau"       lucas.m@example.com      "Standing Desk Frame"      845.00  $N   40
dispute 05 "Sofia Alvarez"      sofia.a@example.com      "Ceramic Pour-Over Kit"     38.00  $N   52
dispute 06 "Ethan Brooks"       ethan.b@example.com      "Wireless Charging Pad"     24.99  $U   80
dispute 07 "Hana Suzuki"        hana.s@example.com       "Espresso Beans 2kg"        54.00  $N  110
dispute 08 "Omar Haddad"        omar.h@example.com       "Carbon Road Helmet"       189.00  $A  140
dispute 09 "Grace Thompson"     grace.t@example.com      "27in 4K Monitor"          349.00  $N  190
dispute 10 "Mateo Rossi"        mateo.r@example.com      "Leather Weekender Bag"    412.00  $U  230
dispute 11 "Aisha Bello"        aisha.b@example.com      "Smart Thermostat"         159.00  $N  300
dispute 12 "Noah Williams"      noah.w@example.com       "Mechanical Keyboard"      134.00  $A  420

echo
echo "Done: 12 disputes, 4 mandates (3 covering, 1 under-authorised)."
echo "They are scored and drafted within a few seconds each; open the console to see them."
