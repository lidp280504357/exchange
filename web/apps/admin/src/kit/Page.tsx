import type { ReactNode } from "react";

/** Page is a section's layout: its title, a line of help and actions, then the content. */
export function Page({ title, help, actions, children }: { title: ReactNode; help?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="text-lg font-semibold">{title}</h1>
          {help && <p className="mt-1 text-sm text-fg-3">{help}</p>}
        </div>
        {actions}
      </div>
      {children}
    </div>
  );
}

/** Card is a bordered panel with an optional title. */
export function Card({ title, extra, children, className }: { title?: ReactNode; extra?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-3 border border-line-1 bg-bg-1 ${className ?? ""}`}>
      {(title || extra) && (
        <div className="flex items-center gap-2 border-b border-line-1 px-4 py-2.5">
          <h2 className="flex-1 text-sm font-medium text-fg-2">{title}</h2>
          {extra}
        </div>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}
