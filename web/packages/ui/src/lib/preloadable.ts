import { createElement, lazy, type ComponentType, type ReactElement } from "react";

// A component in a chunk of its own that a page preloads (a dialog's form,
// B108: core's useIdleImport(C.preload)) and that then renders at once.
// React.lazy suspends on a component's first render even with its chunk
// in, and React holds a Suspense boundary's content back until 300 ms
// after its fallback showed (its reveal throttle), so a preloaded lazy
// dialog took a third of a second to open the first time (B114).

/** A component in a chunk of its own, with the import that preloads it. */
export type Preloadable<P> = ((props: P) => ReactElement) & {
  /** preload imports the chunk (once; again after a failure). */
  preload: () => Promise<unknown>;
};

type Module<P> = { default: ComponentType<P> };

/**
 * preloadable is React.lazy over the component pick takes from the module
 * load imports. Once preload has resolved, the lazy component's import is
 * a thenable that resolves at once, which React renders without
 * suspending; every instance keeps the one element type, so one mounted
 * before the import ended is not remounted after it (review DY, B115). A
 * failed import gets a fresh lazy component for the next mount: React.lazy
 * keeps a failure for good. Define one per chunk and share it, so that a
 * preload anywhere serves every page.
 */
export function preloadable<M, P extends object>(load: () => Promise<M>, pick: (m: M) => ComponentType<P>): Preloadable<P> {
  let loaded: Module<P> | undefined;
  let pending: Promise<M> | undefined;
  const preload = () =>
    (pending ??= load().then(
      (m) => {
        loaded = { default: pick(m) };
        return m;
      },
      (e: unknown) => {
        pending = undefined;
        current = make();
        throw e;
      },
    ));
  const make = () => lazy(() => (loaded ? at(loaded) : preload().then(() => loaded as Module<P>)));
  let current = make();
  const Component = (props: P) => createElement(current, props);
  return Object.assign(Component, { preload });
}

// at is a thenable that hands its value over at once: React.lazy reads the
// module from it during the render instead of suspending.
function at<T>(value: T): Promise<T> {
  return { then: (resolve?: (v: T) => unknown) => resolve?.(value) } as unknown as Promise<T>;
}
