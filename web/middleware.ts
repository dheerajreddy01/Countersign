import { NextResponse, type NextRequest } from "next/server";
import {
  SESSION_COOKIE,
  authConfig,
  checkCredentials,
  verifySession,
} from "@/lib/session";

// The console's gate. A browser signs in at /login and carries a signed
// session cookie (lib/session.ts). Scripts can still send basic auth
// (`curl -u merchant:<password>`), which the deploy smoke checks rely on.
//
// With CONSOLE_USER or CONSOLE_PASSWORD unset the gate is off, so local
// development and the fixtures-only container behave exactly as before.

function basicAuthOk(header: string | null, cfg: NonNullable<ReturnType<typeof authConfig>>) {
  const [scheme, encoded] = (header ?? "").split(" ");
  if (scheme?.toLowerCase() !== "basic" || !encoded) return false;
  let decoded: string;
  try {
    decoded = atob(encoded);
  } catch {
    return false;
  }
  // Only the first colon separates the pair: a password may contain colons.
  const separator = decoded.indexOf(":");
  if (separator < 0) return false;
  return checkCredentials(cfg, decoded.slice(0, separator), decoded.slice(separator + 1));
}

export async function middleware(request: NextRequest) {
  const cfg = authConfig();
  if (!cfg) return NextResponse.next();

  const { pathname, search } = request.nextUrl;
  if (pathname === "/login" || pathname === "/api/login" || pathname === "/api/logout") {
    return NextResponse.next();
  }

  const token = request.cookies.get(SESSION_COOKIE)?.value;
  if (await verifySession(cfg, token)) return NextResponse.next();
  if (basicAuthOk(request.headers.get("authorization"), cfg)) return NextResponse.next();

  // An API call gets a plain 401: no WWW-Authenticate, so a browser fetch does
  // not raise the native credentials popup this page replaces.
  if (pathname.startsWith("/api/")) {
    return NextResponse.json({ error: "sign in required" }, { status: 401 });
  }

  // Middleware redirects must be absolute; nextUrl carries the Host the
  // browser used, so this points back at the public URL, not the container.
  const login = request.nextUrl.clone();
  login.pathname = "/login";
  login.search = `?next=${encodeURIComponent(pathname + search)}`;
  if (request.headers.get("x-forwarded-proto")?.startsWith("https")) login.protocol = "https:";
  return NextResponse.redirect(login);
}

export const config = {
  // Everything except the health probe and Next's own static assets.
  matcher: ["/((?!healthz|_next/static|_next/image|favicon.ico).*)"],
};
