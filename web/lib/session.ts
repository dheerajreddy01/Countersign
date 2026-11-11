// Console sign-in: one shared credential and a signed session cookie.
//
// The console shows every dispute a merchant has, so a deployed URL cannot be
// open. There is still one credential (CONSOLE_USER / CONSOLE_PASSWORD) and no
// user store; what this adds over plain basic auth is a sign-in page and a
// session that can be ended. The cookie holds the user, an expiry and an
// HMAC over both, so the server keeps no session state.
//
// The signing key is CONSOLE_SESSION_SECRET when set, otherwise derived from
// the credential itself: changing the password signs everyone out.
//
// Everything here runs in the edge middleware as well as in route handlers,
// so it uses Web Crypto only.

export const SESSION_COOKIE = "countersign_session";
export const SESSION_TTL_SECONDS = 12 * 60 * 60;

export type AuthConfig = { user: string; password: string; secret: string };

/** The configured credential, or null when the gate is off (local dev). */
export function authConfig(env: Record<string, string | undefined> = process.env): AuthConfig | null {
  const user = env.CONSOLE_USER;
  const password = env.CONSOLE_PASSWORD;
  if (!user || !password) return null;
  return {
    user,
    password,
    secret: env.CONSOLE_SESSION_SECRET || `countersign:${user}:${password}`,
  };
}

/** Length-independent comparison, so a reply cannot be timed character by character. */
export function matches(given: string, expected: string): boolean {
  const a = new TextEncoder().encode(given);
  const b = new TextEncoder().encode(expected);
  let diff = a.length ^ b.length;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    diff |= (a[i] ?? 0) ^ (b[i] ?? 0);
  }
  return diff === 0;
}

/** Both compared every time, so a wrong username costs the same as a wrong password. */
export function checkCredentials(cfg: AuthConfig, user: string, password: string): boolean {
  const userOk = matches(user, cfg.user);
  const passwordOk = matches(password, cfg.password);
  return userOk && passwordOk;
}

function b64url(bytes: Uint8Array): string {
  let s = "";
  for (const byte of bytes) s += String.fromCharCode(byte);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function sign(secret: string, payload: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const mac = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(payload));
  return b64url(new Uint8Array(mac));
}

/** A token of the form `<user>.<expiry>.<mac>`, user base64url-encoded. */
export async function createSession(cfg: AuthConfig, nowMs = Date.now()): Promise<string> {
  const user = b64url(new TextEncoder().encode(cfg.user));
  const exp = Math.floor(nowMs / 1000) + SESSION_TTL_SECONDS;
  const payload = `${user}.${exp}`;
  return `${payload}.${await sign(cfg.secret, payload)}`;
}

export async function verifySession(
  cfg: AuthConfig,
  token: string | undefined,
  nowMs = Date.now(),
): Promise<boolean> {
  if (!token) return false;
  const parts = token.split(".");
  if (parts.length !== 3) return false;
  const [user, expRaw, mac] = parts;
  const exp = Number(expRaw);
  if (!Number.isInteger(exp) || exp * 1000 <= nowMs) return false;
  if (!matches(user, b64url(new TextEncoder().encode(cfg.user)))) return false;
  return matches(mac, await sign(cfg.secret, `${user}.${expRaw}`));
}

/** Where to go after signing in: a path on this site, never another origin. */
export function safeNext(next: unknown): string {
  if (typeof next !== "string" || !next.startsWith("/")) return "/";
  if (next.startsWith("//") || next.startsWith("/\\")) return "/";
  if (next.startsWith("/login") || next.startsWith("/api/")) return "/";
  return next;
}

/** A redirect with a relative Location, so it survives the proxy in front of the app. */
export function seeOther(location: string, headers: HeadersInit = {}): Response {
  const h = new Headers(headers);
  h.set("Location", location);
  return new Response(null, { status: 303, headers: h });
}

export function isHttps(req: Request): boolean {
  const forwarded = req.headers.get("x-forwarded-proto");
  if (forwarded) return forwarded.split(",")[0].trim() === "https";
  return new URL(req.url).protocol === "https:";
}

export function sessionCookie(value: string, maxAge: number, secure: boolean): string {
  return [
    `${SESSION_COOKIE}=${value}`,
    "Path=/",
    "HttpOnly",
    "SameSite=Lax",
    `Max-Age=${maxAge}`,
    secure ? "Secure" : "",
  ]
    .filter(Boolean)
    .join("; ");
}
