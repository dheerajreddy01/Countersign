// The deadline board's rules: what is on it, and which column it goes in.
import { describe, expect, it } from "vitest";
import { bucketFor, buildBoard, isClosed } from "@/lib/board";
import type { CaseStatus, CaseSummary } from "@/lib/types";

// Mid-afternoon, so "later today" and "tomorrow morning" are both well-defined.
const NOW = new Date(2026, 10, 10, 15, 0, 0).getTime(); // Tue Nov 10 2026, 15:00 local
const H = 36e5;
const D = 24 * H;
const at = (ms: number) => new Date(NOW + ms).toISOString();

let n = 0;
function c(over: Partial<CaseSummary> & { respondBy: string | null }): CaseSummary {
  n += 1;
  return {
    id: String(n),
    paypalDisputeId: `PP-D-${n}`,
    reason: "item_not_received",
    stage: "chargeback",
    status: "decision_pending",
    amount: 100,
    currency: "USD",
    buyerName: `Buyer ${n}`,
    productName: "Thing",
    pWin: 0.5,
    recommendation: "fight",
    hasMandate: false,
    ...over,
  };
}

const keys = (cases: CaseSummary[]) => buildBoard(cases, NOW).columns.map((col) => col.key);
const column = (cases: CaseSummary[], key: string) =>
  buildBoard(cases, NOW).columns.find((col) => col.key === key)?.cases.map((x) => x.id) ?? null;

describe("which cases are on the board", () => {
  it("leaves decided cases off, even when they share a date with open ones", () => {
    // The live-data bug: the pipeline keeps respond_by after a ruling, so won
    // and refunded cases sat in the same column as the ones needing action.
    const open = c({ respondBy: at(7 * D) });
    const decided = (["won", "lost", "refunded", "settled"] as CaseStatus[]).map((status) =>
      c({ status, respondBy: at(7 * D) }),
    );
    const board = buildBoard([open, ...decided], NOW);
    const shown = board.columns.flatMap((col) => col.cases.map((x) => x.id));
    expect(shown).toEqual([open.id]);
    expect(board.closed).toBe(4);
  });

  it("keeps every not-yet-decided status on it", () => {
    for (const status of ["open", "evidence_ready", "decision_pending", "action_taken"] as CaseStatus[]) {
      expect(isClosed(status)).toBe(false);
    }
  });
});

describe("columns", () => {
  it("are a fixed horizon, so cases on one day still make a full row", () => {
    const sameDay = [1, 2, 3].map(() => c({ respondBy: at(7 * D) }));
    expect(keys(sameDay)).toEqual(["overdue", "today", "tomorrow", "week", "next", "later"]);
  });

  it("add 'No deadline' only when an open case has none", () => {
    expect(keys([c({ respondBy: at(D) })])).not.toContain("none");
    expect(keys([c({ respondBy: null })])).toContain("none");
  });

  it("bucket by calendar day, overdue by the clock", () => {
    expect(bucketFor(at(-60_000), NOW)).toBe("overdue"); // a minute ago, still today
    expect(bucketFor(at(2 * H), NOW)).toBe("today");
    expect(bucketFor(at(10 * H), NOW)).toBe("tomorrow"); // 01:00 tomorrow, not "today" for being <24h
    expect(bucketFor(at(2 * D), NOW)).toBe("week");
    expect(bucketFor(at(6 * D), NOW)).toBe("week");
    expect(bucketFor(at(7 * D), NOW)).toBe("next");
    expect(bucketFor(at(13 * D), NOW)).toBe("next");
    expect(bucketFor(at(14 * D), NOW)).toBe("later");
    expect(bucketFor(null, NOW)).toBe("none");
    expect(bucketFor("not a date", NOW)).toBe("none");
  });

  it("put the soonest deadline first, then the larger amount", () => {
    const late = c({ respondBy: at(5 * D) });
    const soonSmall = c({ respondBy: at(3 * D), amount: 20 });
    const soonBig = c({ respondBy: at(3 * D), amount: 500 });
    expect(column([late, soonSmall, soonBig], "week")).toEqual([soonBig.id, soonSmall.id, late.id]);
  });

  it("read as calm when empty, urgent when not", () => {
    const empty = buildBoard([], NOW).columns.find((col) => col.key === "overdue")!;
    const full = buildBoard([c({ respondBy: at(-H) })], NOW).columns.find((col) => col.key === "overdue")!;
    expect(empty.urgency).toBe("none");
    expect(full.urgency).toBe("overdue");
  });
});
