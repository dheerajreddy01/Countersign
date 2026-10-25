"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Card, CardBody, CardHeader } from "@/components/ui";
import { GroundingGuardBadge } from "@/components/GroundingGuardBadge";
import { saveDraft } from "@/lib/api";
import { checkGrounding } from "@/lib/grounding";
import { clsx } from "@/lib/clsx";
import type { Draft } from "@/lib/types";

/** Render the draft with [E#] markers styled as chips. */
function renderWithCitations(text: string) {
  const parts = text.split(/(\[E\d+\])/g);
  return parts.map((part, i) => {
    if (/^\[E\d+\]$/.test(part)) {
      return (
        <sup
          key={i}
          className="mx-0.5 rounded bg-indigo-100 px-1 font-mono text-[10px] font-semibold text-indigo-700"
        >
          {part.slice(1, -1)}
        </sup>
      );
    }
    return <span key={i}>{part}</span>;
  });
}

export function DraftPanel({
  caseId,
  draft,
  evidenceIds,
}: {
  caseId: string;
  draft: Draft;
  evidenceIds: string[];
}) {
  const router = useRouter();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(draft.text);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-run the guard live while editing so the merchant sees the effect of a
  // change before saving. This mirrors the server-side check.
  const live = useMemo(
    () => checkGrounding(text, evidenceIds),
    [text, evidenceIds],
  );

  async function onSave() {
    setSaving(true);
    setError(null);
    try {
      await saveDraft(caseId, text);
      setEditing(false);
      router.refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to save draft");
    } finally {
      setSaving(false);
    }
  }

  function onCancel() {
    setText(draft.text);
    setEditing(false);
    setError(null);
  }

  const shown = editing ? live : {
    passed: draft.groundingPassed,
    uncitedSentences: draft.uncitedSentences,
    unknownMarkers: [] as string[],
  };

  return (
    <Card>
      <CardHeader
        title="Drafted response"
        subtitle="The drafter may only state facts that cite an evidence record"
        action={<GroundingGuardBadge passed={shown.passed} />}
      />
      <CardBody className="space-y-3">
        {editing ? (
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={8}
            className="w-full resize-y rounded-lg border border-slate-300 p-3 text-sm leading-relaxed text-slate-800 shadow-inner focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
            placeholder="Cite evidence with [E1], [E2], …"
          />
        ) : (
          <p className="whitespace-pre-wrap text-sm leading-relaxed text-slate-800">
            {renderWithCitations(draft.text)}
          </p>
        )}

        {!shown.passed ? (
          <div className="rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-xs font-semibold text-red-700">
              The grounding guard blocked {shown.uncitedSentences.length}{" "}
              sentence{shown.uncitedSentences.length === 1 ? "" : "s"} with no
              citation:
            </p>
            <ul className="mt-1.5 space-y-1">
              {shown.uncitedSentences.map((s, i) => (
                <li key={i} className="text-xs italic text-red-600">
                  &ldquo;{s}&rdquo;
                </li>
              ))}
            </ul>
            {shown.unknownMarkers.length > 0 ? (
              <p className="mt-1.5 text-xs text-red-600">
                Unknown citation marker(s):{" "}
                {shown.unknownMarkers.map((m) => `[${m}]`).join(", ")}
              </p>
            ) : null}
            <p className="mt-2 text-[11px] text-red-500">
              Add a citation to a stored evidence record, or remove the claim,
              before this response can be submitted.
            </p>
          </div>
        ) : (
          <p className="text-xs text-emerald-600">
            Every factual sentence cites a stored evidence record.
          </p>
        )}

        {error ? (
          <p className="text-xs text-red-600">{error}</p>
        ) : null}

        <div className="flex items-center gap-2 pt-1">
          {editing ? (
            <>
              <button
                onClick={onSave}
                disabled={saving}
                className={clsx(
                  "rounded-lg px-3 py-1.5 text-sm font-medium text-white",
                  "bg-indigo-600 hover:bg-indigo-700 disabled:opacity-50",
                )}
              >
                {saving ? "Saving…" : "Save draft"}
              </button>
              <button
                onClick={onCancel}
                disabled={saving}
                className="rounded-lg px-3 py-1.5 text-sm font-medium text-slate-600 hover:bg-slate-100"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              onClick={() => setEditing(true)}
              className="rounded-lg border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
            >
              Edit draft
            </button>
          )}
        </div>
      </CardBody>
    </Card>
  );
}
