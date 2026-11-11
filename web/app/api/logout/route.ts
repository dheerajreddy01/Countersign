import { isHttps, seeOther, sessionCookie } from "@/lib/session";

// POST /api/logout — clear the session cookie and return to the sign-in page.
export const dynamic = "force-dynamic";

export async function POST(req: Request) {
  return seeOther("/login", { "Set-Cookie": sessionCookie("", 0, isHttps(req)) });
}
