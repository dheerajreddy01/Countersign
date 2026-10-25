// Browser-side helpers the client components use to talk to the route handlers
// under /app/api. Those handlers read/write the in-memory store (or proxy the
// Go console API when CONSOLE_API_URL is set).

import type { ActionKind, DisputeCase } from "./types";

async function parse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status} ${res.statusText}: ${body}`);
  }
  return (await res.json()) as T;
}

export async function saveDraft(
  caseId: string,
  text: string,
): Promise<DisputeCase> {
  const res = await fetch(`/api/cases/${caseId}/draft`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ text }),
  });
  return parse<DisputeCase>(res);
}

export async function submitAction(
  caseId: string,
  kind: ActionKind,
  note: string,
): Promise<DisputeCase> {
  const res = await fetch(`/api/cases/${caseId}/actions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ kind, note }),
  });
  return parse<DisputeCase>(res);
}
