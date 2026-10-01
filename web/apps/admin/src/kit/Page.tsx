import { cn } from "@exchange/ui";
import type { CSSProperties, ReactNode } from "react";

/** Page is a section's layout: its title, a line of help and actions, then the content. */
export function Page({ title, help, actions, children }: { title: ReactNode; help?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {help && <p className="mt-1 text-sm text-fg-3">{help}</p>}
        </div>
        {actions}
      </div>
      {children}
    </div>
  );
}

/** Card is a white panel with a fine border and a soft shadow, with an optional title. */
export function Card({
  title, extra, children, className, style,
}: {
  title?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <section className={cn("card", className)} style={style}>
      {(title || extra) && (
        <div className="flex items-center gap-2 border-b border-line-1 px-4 py-3">
          <h2 className="flex-1 text-sm font-semibold text-fg-1">{title}</h2>
          {extra}
        </div>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}
