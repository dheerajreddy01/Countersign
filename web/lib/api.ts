// Browser-side helpers the client components use to talk to the route handlers
// under /app/api. Those handlers read/write the in-memory store (or proxy the
// Go console API when CONSOLE_API_URL is set).

import type { ActionKind, DisputeCase } from "./types";

async function parse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    throw new Error(await errorMessage(res));
  }
  return (await res.json()) as T;
}

/**
 * The message to show the merchant for a failed request.
 *
 * The route handlers and the case service answer a refusal with
 * `{"error": "..."}` written for the merchant ("this case is already closed",
 * "another action on this case is still being processed"), so that sentence is
 * shown as it is. Anything else (a proxy's HTML page, an empty body) falls back
 * to the status line, which is still more use than a bare "failed".
 */
export async function errorMessage(res: Response): Promise<string> {
  const text = await res.text().catch(() => "");
  try {
    const body = JSON.parse(text) as unknown;
    if (body && typeof body === "object" && "error" in body) {
      const error = (body as { error: unknown }).error;
      if (typeof error === "string" && error.trim()) return error;
    }
  } catch {
    // Not JSON: fall through to the status line.
  }
  const status = `${res.status}${res.statusText ? ` ${res.statusText}` : ""}`;
  return text.trim() ? `Request failed (${status}): ${text.trim().slice(0, 200)}` : `Request failed (${status})`;
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

export async function generateDraft(caseId: string): Promise<DisputeCase> {
  const res = await fetch(`/api/cases/${caseId}/draft`, { method: "POST" });
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
