import { Button, cn } from "@exchange/ui";
import { Check } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type SectionState = "active" | "done" | "locked";

/**
 * StepSection is one numbered part of a form laid out as a vertical
 * timeline (the withdrawal): open while it is being filled, a one-line
 * summary with "change" once done, a hint while an earlier part is missing.
 */
export function StepSection({
  n, title, state, summary, onChange, last, children,
}: {
  n: number;
  title: ReactNode;
  state: SectionState;
  summary?: ReactNode;
  onChange?: () => void;
  last?: boolean;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section aria-label={typeof title === "string" ? title : undefined} className={cn("relative pl-12", !last && "pb-7")}>
      {!last && (
        <span
          aria-hidden
          className={cn("absolute bottom-1 left-4 top-10 w-px transition-colors duration-[var(--t-slow)]", state === "done" ? "bg-brand" : "bg-line-2")}
        />
      )}
      <span
        aria-hidden
        className={cn(
          "absolute left-0 top-0 grid size-8 place-items-center rounded-full border text-sm font-semibold tabular-nums transition-colors duration-[var(--t-base)]",
          state === "done" && "border-brand bg-brand text-brand-fg",
          state === "active" && "border-brand bg-brand-soft text-brand",
          state === "locked" && "border-line-2 bg-bg-2 text-fg-3",
        )}
      >
        {state === "done" ? <Check size={14} strokeWidth={3} /> : n}
      </span>
      <div className="flex min-h-8 items-center justify-between gap-3">
        <h2 className={cn("text-base font-medium", state === "locked" ? "text-fg-3" : "text-fg-1")}>
          <span className="sr-only">{t("pcAssets.common.step", { n })} </span>
          {title}
        </h2>
        {state === "done" && onChange && (
          <Button size="sm" variant="ghost" onClick={onChange}>
            {t("pcAssets.common.change")}
          </Button>
        )}
      </div>
      {state === "done" && summary && <div className="mt-2">{summary}</div>}
      {state === "active" && <div className="mt-3 animate-fade-up">{children}</div>}
      {state === "locked" && <p className="mt-1 text-sm text-fg-3">{t("pcAssets.common.locked")}</p>}
    </section>
  );
}
