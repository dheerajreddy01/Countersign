// What the merchant reads when an action is refused. The case service answers
// a double-click or an action on a closed case with 409 and a sentence in
// {"error": ...}; that sentence, not a dump of the response, is the message.
import { afterEach, describe, expect, it, vi } from "vitest";
import { submitAction } from "@/lib/api";

function respond(status: number, statusText: string, body: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(body, { status, statusText })),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("submitAction errors", () => {
  it("shows the case service's reason for a 409 as written", async () => {
    respond(
      409,
      "Conflict",
      JSON.stringify({
        error:
          "another action on this case is still being processed; wait for it to finish before taking another",
      }),
    );
    await expect(submitAction("7", "accept_claim", "")).rejects.toThrow(
      /^another action on this case is still being processed; wait for it to finish before taking another$/,
    );
  });

  it("shows the closed-case refusal as written", async () => {
    respond(
      409,
      "Conflict",
      JSON.stringify({
        error: "this case is already closed (won); no further actions can be taken on it",
      }),
    );
    await expect(submitAction("7", "accept_claim", "")).rejects.toThrow(
      /^this case is already closed \(won\); no further actions can be taken on it$/,
    );
  });

  it("falls back to the status when the body is not an error object", async () => {
    respond(502, "Bad Gateway", "<html>upstream down</html>");
    await expect(submitAction("7", "make_offer", "")).rejects.toThrow(
      /Request failed \(502 Bad Gateway\)/,
    );
  });
});
