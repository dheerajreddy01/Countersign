import { Badge } from "@/components/ui";

/** Compact pass/fail indicator for the grounding guard. */
export function GroundingGuardBadge({ passed }: { passed: boolean }) {
  return passed ? (
    <Badge tone="green">
      <span aria-hidden>✓</span> Grounding guard passed
    </Badge>
  ) : (
    <Badge tone="red">
      <span aria-hidden>✕</span> Grounding guard blocked
    </Badge>
  );
}
