#!/usr/bin/env bash
# Wake the deployed demo before showing it.
#
# The console, gateway and mandate services run on Render's free instance
# type, which sleeps after 15 idle minutes and takes about a minute to wake.
# Run this a couple of minutes before a demo: it pings each service's
# /healthz in parallel and waits until all three answer, so the first page
# load in front of an audience is instant. They stay awake for 15 minutes
# after the last request.
#
#   scripts/warm-up.sh            # the deployed services
#   SUFFIX=abcd scripts/warm-up.sh  # a deploy with a different name suffix
set -euo pipefail

SUFFIX="${SUFFIX:-yhhv}"
SERVICES=(console gateway mandate)
TIMEOUT="${TIMEOUT:-180}"

wake() {
  local url="https://countersign-$1-${SUFFIX}.onrender.com/healthz"
  local start=$SECONDS
  while (( SECONDS - start < TIMEOUT )); do
    if curl -fsS -o /dev/null --max-time 30 "$url" 2>/dev/null; then
      printf '  %-8s up after %ss\n' "$1" "$(( SECONDS - start ))"
      return 0
    fi
    sleep 5
  done
  printf '  %-8s NOT up after %ss: %s\n' "$1" "$TIMEOUT" "$url"
  return 1
}

echo "Waking ${SERVICES[*]} (free instances take up to a minute)..."
pids=()
for s in "${SERVICES[@]}"; do
  wake "$s" &
  pids+=($!)
done

failed=0
for p in "${pids[@]}"; do wait "$p" || failed=1; done

if (( failed )); then
  echo "Some services did not wake; check the Render dashboard." >&2
  exit 1
fi
echo "All up. They stay warm for 15 minutes after the last request."
