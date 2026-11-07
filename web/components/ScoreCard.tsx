import { Badge, Card, CardBody, CardHeader } from "@/components/ui";
import { FactorBars } from "@/components/FactorBars";
import { WinProbabilityGauge } from "@/components/WinProbabilityGauge";
import { formatPercent } from "@/lib/format";
import type { Decision } from "@/lib/types";

/** Win probability, the top factors, and the fraud signal when present. */
export function ScoreCard({ decision }: { decision: Decision }) {
  const fraud = decision.fraudLikelihood;

  return (
    <Card>
      <CardHeader
        title="Chance of winning"
        subtitle="Scored from published win-rate estimates (logistic regression v1)"
        action={
          Object.keys(decision.modelVersions).length > 0 ? (
            <Badge tone="neutral">
              {Object.entries(decision.modelVersions)
                .map(([k, v]) => `${k} ${v}`)
                .join(" · ")}
            </Badge>
          ) : null
        }
      />
      <CardBody>
        <div className="grid gap-6 sm:grid-cols-[auto_1fr] sm:items-center">
          <WinProbabilityGauge pWin={decision.pWin} />
          <div>
            <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-500">
              What moves this case
            </p>
            <FactorBars factors={decision.factors} />
          </div>
        </div>

        {fraud != null ? (
          <div className="mt-5 flex items-center justify-between rounded-lg border border-slate-200 bg-slate-50 px-3 py-2.5">
            <div>
              <p className="text-sm font-medium text-slate-700">
                Fraud likelihood
              </p>
              <p className="text-xs text-slate-500">
                How much this looks like real card fraud. Used only when the
                buyer says they did not authorise the charge.
              </p>
            </div>
            <Badge tone={fraud >= 0.5 ? "red" : "green"}>
              {formatPercent(fraud, 0)}
            </Badge>
          </div>
        ) : null}

        <p className="mt-4 text-xs text-slate-400">
          Probabilities are estimates, not measurements of a real merchant&apos;s
          results. Sandbox outcomes are simulated.
        </p>
      </CardBody>
    </Card>
  );
}
