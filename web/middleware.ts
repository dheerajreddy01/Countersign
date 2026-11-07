import { NextResponse, type NextRequest } from "next/server";

// Basic auth for the console.
//
// The console shows every dispute a merchant has, so a deployed URL cannot be
// open. This is the smallest gate that actually closes: one shared credential,
// checked at the edge, with no session handling or user store to get wrong. It
// is not a substitute for real accounts — it keeps a demo deployment private
// until the console can act on a dispute and needs to know who acted.
//
// With CONSOLE_USER or CONSOLE_PASSWORD unset the gate is off, so local
// development and the fixtures-only container behave exactly as before.

const REALM = "Countersign console";

/** Length-independent comparison, so a reply cannot be timed character by character. */
function matches(given: string, expected: string): boolean {
  const a = new TextEncoder().encode(given);
  const b = new TextEncoder().encode(expected);
  let diff = a.length ^ b.length;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    diff |= (a[i] ?? 0) ^ (b[i] ?? 0);
  }
  return diff === 0;
}

function unauthorized() {
  return new NextResponse("Authentication required", {
    status: 401,
    headers: { "WWW-Authenticate": `Basic realm="${REALM}", charset="UTF-8"` },
  });
}

export function middleware(request: NextRequest) {
  const user = process.env.CONSOLE_USER;
  const password = process.env.CONSOLE_PASSWORD;
  if (!user || !password) return NextResponse.next();

  const header = request.headers.get("authorization") ?? "";
  const [scheme, encoded] = header.split(" ");
  if (scheme?.toLowerCase() !== "basic" || !encoded) return unauthorized();

  let decoded: string;
  try {
    decoded = atob(encoded);
  } catch {
    return unauthorized();
  }

  // Only the first colon separates the pair: a password may contain colons.
  const separator = decoded.indexOf(":");
  if (separator < 0) return unauthorized();
  const givenUser = decoded.slice(0, separator);
  const givenPassword = decoded.slice(separator + 1);

  // Both compared every time, so a wrong username costs the same as a wrong
  // password.
  const ok = matches(givenUser, user) && matches(givenPassword, password);
  return ok ? NextResponse.next() : unauthorized();
}

export const config = {
  // Everything except the health probe and Next's own static assets.
  matcher: ["/((?!healthz|_next/static|_next/image|favicon.ico).*)"],
};
