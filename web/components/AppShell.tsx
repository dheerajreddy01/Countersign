import Link from "next/link";

/** The app frame: a header with the product mark and a thin status strip. */
export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 border-b border-slate-200 bg-white/80 backdrop-blur">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-4 py-3 sm:px-6">
          <Link href="/" className="flex items-center gap-2">
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-indigo-600 font-mono text-sm font-bold text-white">
              C
            </span>
            <span className="text-sm font-semibold text-slate-900">
              Countersign
            </span>
            <span className="hidden text-xs text-slate-400 sm:inline">
              merchant console
            </span>
          </Link>
          <div className="flex items-center gap-3 text-xs text-slate-500">
            <span className="hidden items-center gap-1.5 sm:flex">
              <span className="h-2 w-2 rounded-full bg-emerald-400" />
              PayPal sandbox
            </span>
            <span className="rounded-md bg-slate-100 px-2 py-1 font-medium text-slate-600">
              Demo data
            </span>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6">{children}</main>
      <footer className="mx-auto max-w-7xl px-4 pb-8 pt-4 text-xs text-slate-400 sm:px-6">
        Probabilities are estimates from published figures, not measurements.
        Sandbox outcomes are simulated and labelled as such.
      </footer>
    </div>
  );
}
