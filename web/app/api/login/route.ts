import {
  SESSION_TTL_SECONDS,
  authConfig,
  checkCredentials,
  createSession,
  isHttps,
  safeNext,
  seeOther,
  sessionCookie,
} from "@/lib/session";

// POST /api/login — the sign-in form posts here as an ordinary form, so it
// works without JavaScript. Success sets the session cookie and returns to
// where the merchant was going; failure goes back to the form with an error.
export const dynamic = "force-dynamic";

const FAILURE_DELAY_MS = 600;

export async function POST(req: Request) {
  const cfg = authConfig();
  const form = await req.formData().catch(() => null);
  const next = safeNext(form?.get("next"));
  if (!cfg) return seeOther(next);

  const user = String(form?.get("username") ?? "");
  const password = String(form?.get("password") ?? "");

  if (!checkCredentials(cfg, user, password)) {
    // A fixed pause on every failure makes guessing slow without a lockout
    // that would let anyone lock the merchant out.
    await new Promise((r) => setTimeout(r, FAILURE_DELAY_MS));
    return seeOther(`/login?error=1&next=${encodeURIComponent(next)}`);
  }

  const token = await createSession(cfg);
  return seeOther(next, {
    "Set-Cookie": sessionCookie(token, SESSION_TTL_SECONDS, isHttps(req)),
  });
}
