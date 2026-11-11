import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { authConfig, safeNext } from "@/lib/session";

export const metadata: Metadata = { title: "Sign in · Countersign console" };
export const dynamic = "force-dynamic";

// The sign-in page. It covers the app shell rather than living outside it, so
// the header and navigation are not shown to someone who has not signed in.
export default function LoginPage({
  searchParams,
}: {
  searchParams: { next?: string; error?: string };
}) {
  const next = safeNext(searchParams.next);
  if (!authConfig()) redirect(next);
  const failed = searchParams.error === "1";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center overflow-y-auto bg-slate-50 px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-indigo-600 font-mono text-lg font-bold text-white shadow-card">
            C
          </span>
          <div>
            <h1 className="text-lg font-semibold text-slate-900">Sign in to Countersign</h1>
            <p className="mt-1 text-sm text-slate-500">Merchant console</p>
          </div>
        </div>

        <form
          method="post"
          action="/api/login"
          className="space-y-4 rounded-xl border border-slate-200 bg-white p-6 shadow-card"
        >
          <input type="hidden" name="next" value={next} />

          {failed && (
            <p
              role="alert"
              className="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700"
            >
              That username and password don&apos;t match. Try again.
            </p>
          )}

          <div className="space-y-1.5">
            <label htmlFor="username" className="block text-sm font-medium text-slate-700">
              Username
            </label>
            <input
              id="username"
              name="username"
              type="text"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              required
              autoFocus
              className="block w-full rounded-md border border-slate-300 px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20"
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="password" className="block text-sm font-medium text-slate-700">
              Password
            </label>
            <input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              className="block w-full rounded-md border border-slate-300 px-3 py-2 text-sm text-slate-900 shadow-sm outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20"
            />
          </div>

          <button
            type="submit"
            className="w-full rounded-md bg-indigo-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-indigo-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2"
          >
            Sign in
          </button>
        </form>

        <p className="mt-4 text-center text-xs text-slate-400">
          Sessions last 12 hours.
        </p>
      </div>
    </div>
  );
}
