// What the deadline board shows, decided away from the component so it can be
// tested without rendering.
//
// Two rules the first version got wrong against live data:
//
// 1. A decided case is not on the board. The pipeline keeps a case's
//    respond-by date after it is won, lost, settled or refunded, so bucketing
//    every case by date put finished disputes in the same columns as the ones
//    still waiting on the merchant — 20 cards under a "12 open" tile.
// 2. The columns are a fixed horizon, not one per distinct date. When cases
//    clustered on one day the board was a single narrow column and an empty
//    row; a fixed set of buckets keeps it a board however the dates fall.

import type { Urgency } from "@/lib/format";
import type { CaseStatus, CaseSummary } from "@/lib/types";

/** Statuses after which there is nothing left for the merchant to do. */
export const CLOSED_STATUSES: readonly CaseStatus[] = ["won", "lost", "refunded", "settled"];

export function isClosed(status: CaseStatus): boolean {
  return CLOSED_STATUSES.includes(status);
}

export type BucketKey = "overdue" | "today" | "tomorrow" | "week" | "next" | "later" | "none";

export interface BoardColumn {
  key: BucketKey;
  label: string;
  sub: string;
  urgency: Urgency;
  cases: CaseSummary[];
}

export interface Board {
  columns: BoardColumn[];
  /** Decided cases left off the board. */
  closed: number;
}

const DAY = 24 * 36e5;

function startOfDay(t: number): number {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/** Whole calendar days from today to `t`; DST-safe because of the rounding. */
function daysFromToday(t: number, now: number): number {
  return Math.round((startOfDay(t) - startOfDay(now)) / DAY);
}

export function bucketFor(respondBy: string | null, now: number): BucketKey {
  if (respondBy === null) return "none";
  const t = new Date(respondBy).getTime();
  if (Number.isNaN(t)) return "none";
  if (t < now) return "overdue";
  const days = daysFromToday(t, now);
  if (days <= 0) return "today";
  if (days === 1) return "tomorrow";
  if (days <= 6) return "week";
  if (days <= 13) return "next";
  return "later";
}

const fmtDay = (t: number) => new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(t);

function range(now: number, fromDays: number, toDays: number): string {
  const from = startOfDay(now) + fromDays * DAY;
  const to = startOfDay(now) + toDays * DAY;
  const a = new Date(from);
  const b = new Date(to);
  // "Nov 12 – 16" within a month, "Nov 28 – Dec 2" across one.
  return a.getMonth() === b.getMonth()
    ? `${fmtDay(from)} – ${b.getDate()}`
    : `${fmtDay(from)} – ${fmtDay(to)}`;
}

/** Soonest deadline first, then the larger amount: the order a merchant works in. */
function byDeadline(a: CaseSummary, b: CaseSummary): number {
  const ta = a.respondBy ? new Date(a.respondBy).getTime() : Infinity;
  const tb = b.respondBy ? new Date(b.respondBy).getTime() : Infinity;
  if (ta !== tb) return ta - tb;
  if (a.amount !== b.amount) return b.amount - a.amount;
  return a.id.localeCompare(b.id);
}

export function buildBoard(cases: CaseSummary[], now: number): Board {
  const open = cases.filter((c) => !isClosed(c.status));
  const sorted = [...open].sort(byDeadline);
  const groups = new Map<BucketKey, CaseSummary[]>();
  for (const c of sorted) {
    const key = bucketFor(c.respondBy, now);
    groups.set(key, [...(groups.get(key) ?? []), c]);
  }
  const get = (k: BucketKey) => groups.get(k) ?? [];
  // An empty column reads as calm, not as an alarm.
  const tone = (k: BucketKey, u: Urgency): Urgency => (get(k).length ? u : "none");

  const columns: BoardColumn[] = [
    { key: "overdue", label: "Overdue", sub: "past respond-by", urgency: tone("overdue", "overdue"), cases: get("overdue") },
    { key: "today", label: "Today", sub: fmtDay(now), urgency: tone("today", "critical"), cases: get("today") },
    { key: "tomorrow", label: "Tomorrow", sub: fmtDay(startOfDay(now) + DAY), urgency: tone("tomorrow", "soon"), cases: get("tomorrow") },
    { key: "week", label: "This week", sub: range(now, 2, 6), urgency: tone("week", "soon"), cases: get("week") },
    { key: "next", label: "Next week", sub: range(now, 7, 13), urgency: tone("next", "comfortable"), cases: get("next") },
    { key: "later", label: "Later", sub: `${fmtDay(startOfDay(now) + 14 * DAY)} onward`, urgency: tone("later", "comfortable"), cases: get("later") },
  ];
  // Rare for an open dispute, so only shown when it has something in it.
  if (get("none").length) {
    columns.push({ key: "none", label: "No deadline", sub: "no respond-by date", urgency: "none", cases: get("none") });
  }
  return { columns, closed: cases.length - open.length };
}
