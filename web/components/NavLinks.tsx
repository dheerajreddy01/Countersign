"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const LINKS = [
  { href: "/", label: "Board" },
  { href: "/cases", label: "All cases" },
];

/** Header navigation, with the section you are in marked. */
export function NavLinks() {
  const pathname = usePathname();

  return (
    <nav className="flex items-center gap-1" aria-label="Main">
      {LINKS.map((link) => {
        // A case detail page belongs to the cases section, so /cases/12 keeps
        // "All cases" marked rather than leaving the header looking unvisited.
        const active =
          link.href === "/"
            ? pathname === "/"
            : pathname === link.href || pathname.startsWith(`${link.href}/`);
        return (
          <Link
            key={link.href}
            href={link.href}
            aria-current={active ? "page" : undefined}
            className={
              active
                ? "rounded-md bg-slate-100 px-2.5 py-1 text-sm font-medium text-slate-900"
                : "rounded-md px-2.5 py-1 text-sm text-slate-500 hover:bg-slate-50 hover:text-slate-800"
            }
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}
