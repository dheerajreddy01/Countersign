#!/bin/bash
# Creates the Kafka topics listed in PLAN.md. Safe to re-run.
set -euo pipefail

topics=(
  pp.webhooks.raw
  cs.dispute.events
  cs.evidence.requested
  cs.case.ready
  cs.decision.pending
  cs.action.requested
  cs.outcomes
  cs.dlq
)

for t in "${topics[@]}"; do
  rpk topic create "$t" --partitions 3 --replicas 1 || true
done

rpk topic list
