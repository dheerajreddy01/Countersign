import Link from "next/link";

export default function NotFound() {
  return (
    <div className="mx-auto max-w-md py-20 text-center">
      <p className="text-sm font-medium text-indigo-600">404</p>
      <h1 className="mt-2 text-xl font-semibold text-slate-900">
        Case not found
      </h1>
      <p className="mt-1 text-sm text-slate-500">
        This dispute may have been resolved or never existed.
      </p>
      <Link
        href="/"
        className="mt-4 inline-block rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
      >
        Back to disputes
      </Link>
    </div>
  );
}
