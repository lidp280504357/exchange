import { useEffect, useState } from "react";

// Visibility of list rows (the sparklines load when their row shows): one
// shared IntersectionObserver per margin for the whole page instead of one
// per row. Without IntersectionObserver (tests, old browsers) everything
// counts as visible.

type Pool = { io: IntersectionObserver; targets: Map<Element, (visible: boolean) => void> };

const pools = new Map<string, Pool>();

/** observeVisibility reports whether el is (near) the viewport; returns the stop. */
export function observeVisibility(el: Element, onChange: (visible: boolean) => void, rootMargin = "160px"): () => void {
  if (typeof IntersectionObserver === "undefined") {
    onChange(true);
    return () => {};
  }
  let pool = pools.get(rootMargin);
  if (!pool) {
    const targets = new Map<Element, (visible: boolean) => void>();
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) targets.get(e.target)?.(e.isIntersecting);
      },
      { rootMargin },
    );
    pool = { io, targets };
    pools.set(rootMargin, pool);
  }
  const p = pool;
  p.targets.set(el, onChange);
  p.io.observe(el);
  return () => {
    p.targets.delete(el);
    p.io.unobserve(el);
  };
}

export type InViewOptions = {
  /** How far outside the viewport still counts (CSS margin, default 160px). */
  rootMargin?: string;
  /** Stay true after the first sighting and stop watching (default true). */
  once?: boolean;
};

/**
 * useInView returns a ref callback and whether its element is in view:
 * `const [ref, seen] = useInView<HTMLDivElement>()`.
 */
export function useInView<T extends Element>({ rootMargin = "160px", once = true }: InViewOptions = {}): [(el: T | null) => void, boolean] {
  const [el, setEl] = useState<T | null>(null);
  const [inView, setInView] = useState(false);
  const done = once && inView;
  useEffect(() => {
    if (!el || done) return;
    return observeVisibility(el, setInView, rootMargin);
  }, [el, rootMargin, done]);
  return [setEl, inView];
}
