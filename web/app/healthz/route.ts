// GET /healthz — a liveness probe for the platform.
//
// Deliberately outside the basic-auth gate in middleware.ts: a health check
// that answers 401 reads as a failed deploy.
export const dynamic = "force-dynamic";

export async function GET() {
  return new Response("ok", {
    status: 200,
    headers: { "content-type": "text/plain" },
  });
}
