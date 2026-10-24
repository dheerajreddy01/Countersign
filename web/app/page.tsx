import { CaseTable } from "@/components/CaseTable";
import { DeadlineBoard } from "@/components/DeadlineBoard";
import { StatStrip, type Stat } from "@/components/StatStrip";
import { formatMoney } from "@/lib/format";
import { listCases } from "@/lib/store";

// Read the live store on every request so approvals/edits are reflected.
export const dynamic = "force-dynamic";

export default function DashboardPage() {
  const cases = listCases();
  const now = Date.now();

  const openCases = cases.filter(
    (c) => !["won", "lost", "refunded", "settled"].includes(c.status),
  );
  const needsReview = cases.filter((c) => c.status === "decision_pending");
  const overdue = openCases.filter(
    (c) => c.respondBy !== null && new Date(c.respondBy).getTime() < now,
  );
  const atRisk = openCases.reduce((sum, c) => sum + c.amount, 0);

  const stats: Stat[] = [
    { label: "Open cases", value: String(openCases.length), hint: `${cases.length} total` },
    { label: "Amount at risk", value: formatMoney(atRisk), hint: "across open cases" },
    {
      label: "Needs review",
      value: String(needsReview.length),
      hint: "awaiting your approval",
      tone: needsReview.length > 0 ? "amber" : "default",
    },
    {
      label: "Overdue",
      value: String(overdue.length),
      hint: "past the respond-by deadline",
      tone: overdue.length > 0 ? "red" : "green",
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold text-slate-900">Disputes</h1>
        <p className="text-sm text-slate-500">
          Review each case, check the drafted response, and act before the
          deadline.
        </p>
      </div>

      <StatStrip stats={stats} />

      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-800">
            Deadline board
          </h2>
          <p className="text-xs text-slate-400">
            Bucketed by respond-by date · colored by urgency
          </p>
        </div>
        <DeadlineBoard cases={cases} />
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-slate-800">All cases</h2>
        <CaseTable cases={cases} />
      </section>
    </div>
  );
}
