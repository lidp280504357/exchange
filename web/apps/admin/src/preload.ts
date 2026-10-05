// preloadConsole starts downloading the signed-in shell's chunk and the
// chunk of the page at the address while /admin/v1/me is asked, instead of
// one after the other once the session is known (C6: the overview scored 83
// for performance on Lighthouse, its chunks three round trips apart). The
// console's CSP forbids inline scripts, so index.html cannot do it as the
// user sites do (web/scripts/route-preload.mjs); the entry does. The pages
// are pageLoaders.tsx's: a page loaded here renders at once when shown.

import { pageAt, pagesIdle } from "./pageLoaders";

const fail = () => undefined; // the page's own render reports a failure

/** preloadConsole starts the chunks a signed-in visit to pathname needs; nothing for the sign-in and setup pages. */
export function preloadConsole(pathname: string) {
  if (/^\/*(login|setup)(\/|$)/.test(pathname)) return;
  void import("./layout/SignedIn").catch(fail);
  void pageAt(pathname)?.preload().catch(fail);
}

/** prefetchPage starts the chunk of the page at path: a sidebar link pointed at or focused (A40). */
export function prefetchPage(path: string) {
  void pageAt(path)?.preload().catch(fail);
}

/**
 * prefetchPages loads the chunks of the pages at paths one after another
 * while the browser has nothing else to do (A40): a section opened later
 * finds its page loaded. It waits while the tab is hidden and while a page
 * the administrator opened is still coming, skips it all when the browser
 * asks to save data, and stops when the returned function is called (the
 * sidebar unmounts: signed out, or React's development double run).
 */
export function prefetchPages(paths: readonly string[]): () => void {
  if ((navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData) return () => undefined;
  let stopped = false;
  let idleHandle: number | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const queue = [...paths];
  const schedule = () => {
    if (stopped) return;
    if (typeof requestIdleCallback === "function") idleHandle = requestIdleCallback(() => void next(), { timeout: 5000 });
    else timer = setTimeout(() => void next(), 200);
  };
  const next = async () => {
    if (stopped) return;
    if (document.visibilityState === "hidden") {
      document.addEventListener("visibilitychange", schedule, { once: true });
      return;
    }
    await pagesIdle();
    const path = queue.shift();
    if (stopped || path === undefined) return;
    await pageAt(path)?.preload().catch(fail);
    schedule();
  };
  schedule();
  return () => {
    stopped = true;
    if (idleHandle !== undefined && typeof cancelIdleCallback === "function") cancelIdleCallback(idleHandle);
    if (timer !== undefined) clearTimeout(timer);
    document.removeEventListener("visibilitychange", schedule);
  };
}
