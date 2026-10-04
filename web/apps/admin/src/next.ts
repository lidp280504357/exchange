/**
 * nextPath is where a sign-in returns to: the console path in ?next= (set
 * when a visitor was sent to sign in), "/" when there is none or it is
 * not one of the console's own.
 */
export function nextPath(search: string): string {
  const next = new URLSearchParams(search).get("next");
  return next && next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") && !next.startsWith("/login") ? next : "/";
}
