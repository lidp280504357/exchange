import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { Button } from "./Button";

// Every data block has three states besides its content (design §4.3):
// loading (Skeleton), empty (an illustration and a way forward) and error
// (why, and retry). "Nothing here" never stands in for "still loading".

export type EmptyStateProps = {
  title?: ReactNode;
  description?: ReactNode;
  /** A call to action, e.g. a deposit button. */
  action?: ReactNode;
  compact?: boolean;
  className?: string;
};

/** EmptyState: an illustration, a title, a hint and an action. */
export function EmptyState({ title, description, action, compact, className }: EmptyStateProps) {
  const { t } = useTranslation();
  return (
    <div className={cn("flex flex-col items-center justify-center text-center", compact ? "gap-2 py-6" : "gap-3 py-12", className)}>
      <EmptyArt size={compact ? 56 : 88} />
      <div className="text-base font-medium text-fg-1">{title ?? t("state.emptyTitle")}</div>
      {description && <div className="max-w-xs text-sm text-fg-3">{description}</div>}
      {action}
    </div>
  );
}

export type ErrorStateProps = {
  title?: ReactNode;
  /** The error's text (from errorText) and its trace ID if any. */
  message?: ReactNode;
  traceId?: string;
  onRetry?: () => void;
  compact?: boolean;
  className?: string;
};

/** ErrorState: what failed, the trace ID to quote, and retry. */
export function ErrorState({ title, message, traceId, onRetry, compact, className }: ErrorStateProps) {
  const { t } = useTranslation();
  return (
    <div role="alert" className={cn("flex flex-col items-center justify-center gap-2 text-center", compact ? "py-6" : "py-12", className)}>
      <div className="grid size-10 place-items-center rounded-full bg-bg-3 text-lg text-danger">!</div>
      <div className="text-base font-medium text-fg-1">{title ?? t("state.errorTitle")}</div>
      <div className="max-w-sm text-sm text-fg-3">{message ?? t("state.errorHint")}</div>
      {traceId && <code className="text-xs text-fg-3">trace {traceId.slice(0, 12)}</code>}
      {onRetry && (
        <Button size="sm" variant="secondary" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}

/** EmptyArt is the empty-state illustration: a tray with a soft glow. */
export function EmptyArt({ size = 88 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 88 88" fill="none" aria-hidden>
      <circle cx="44" cy="44" r="40" className="fill-bg-2" />
      <circle cx="44" cy="44" r="28" className="fill-glow" />
      <path d="M22 50l7-17h30l7 17v10a4 4 0 0 1-4 4H26a4 4 0 0 1-4-4V50z" className="fill-bg-3 stroke-line-2" strokeWidth="2" />
      <path d="M22 50h14l3 5h10l3-5h14" className="stroke-line-2" strokeWidth="2" strokeLinecap="round" />
      <circle cx="60" cy="28" r="3" className="fill-brand" />
    </svg>
  );
}
