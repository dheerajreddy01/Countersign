// Tiny classnames joiner, so the UI has no runtime dependency for it.
export type ClassValue =
  | string
  | number
  | null
  | false
  | undefined
  | ClassValue[];

export function clsx(...values: ClassValue[]): string {
  const out: string[] = [];
  for (const v of values) {
    if (!v) continue;
    if (Array.isArray(v)) {
      const inner = clsx(...v);
      if (inner) out.push(inner);
    } else {
      out.push(String(v));
    }
  }
  return out.join(" ");
}
