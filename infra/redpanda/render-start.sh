#!/usr/bin/env bash
# Start Redpanda, create the pipeline's topics once it is healthy, then stay
# attached to the broker for the life of the container.
#
# Why this exists: every service reaches Redpanda only through its HTTP proxy,
# and the proxy never creates a topic on produce, whatever
# auto_create_topics_enabled says — a produce to a missing topic returns
# error_code 3 and nothing is created. Without this step the gateway answers
# every webhook with 502 and no dispute ever enters the pipeline. Compose has an
# init job for it; a Render Blueprint has no one-off task, so the broker does it.
#
# create-topics.sh is idempotent (each create is `|| true`), which matters: this
# runs on every boot, and with the disk attached the topics usually exist.
set -euo pipefail
export RPK_BROKERS=127.0.0.1:9092

/usr/bin/rpk redpanda start "$@" &
broker=$!

# Render sends SIGTERM on every redeploy: pass it on and let the broker flush
# and stop cleanly instead of being killed when the grace period runs out.
trap 'kill -TERM "$broker" 2>/dev/null; wait "$broker"; exit $?' TERM INT

until rpk cluster health 2>/dev/null | grep -q 'Healthy:.*true'; do
  if ! kill -0 "$broker" 2>/dev/null; then
    echo "render-start: redpanda exited before becoming healthy" >&2
    wait "$broker"
    exit $?
  fi
  sleep 1
done

for attempt in $(seq 1 30); do
  if bash /opt/countersign/create-topics.sh; then
    echo "render-start: topics ready"
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    # Exiting stops the container and the platform restarts it, which retries
    # this whole sequence. Better than a broker with no topics that looks up.
    echo "render-start: could not create topics after 30 attempts" >&2
    kill -TERM "$broker" 2>/dev/null
    wait "$broker" || true
    exit 1
  fi
  sleep 2
done

wait "$broker"
