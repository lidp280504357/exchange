import { errorText } from "@exchange/core";
import { Skeleton, Spinner, cn, listItem } from "@exchange/ui";
import { ChevronRight, RotateCcw } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { TextButton } from "../../auth/parts/TextButton";
import { entrance } from "./logic";

export type SecurityCardProps = {
  icon: ReactNode;
  title: ReactNode;
  desc: ReactNode;
  /** The status badge next to the title. */
  status?: ReactNode;
  /** The current value (a masked email, a count). */
  detail?: ReactNode;
  /** What a tap does ("绑定", "修改"), shown with a chevron. */
  action?: ReactNode;
  /** The action adds protection: it reads in the brand colour. */
  emphasis?: boolean;
  onClick?: () => void;
  /** A link instead of a button (devices, history). */
  to?: string;
  /** The action is starting (a step-up is asked for). */
  busy?: boolean;
  /** The card's data is loading: skeletons stand in for status and action. */
  loading?: boolean;
  /** The card's data failed: its text and a retry stand in for them. */
  error?: unknown;
  onRetry?: () => void;
  /** Its place in the list, for the first-screen stagger. */
  index: number;
};

/**
 * SecurityCard is one item of the security centre on the phone: an icon,
 * the title with its status, what it is for, and a bottom line with the
 * current value and the action. The whole card is the tap target; while
 * its data fails it is not, and a retry shows instead.
 */
export function SecurityCard({
  icon, title, desc, status, detail, action, emphasis, onClick, to, busy, loading, error, onRetry, index,
}: SecurityCardProps) {
  const failed = error != null;
  const body = (
    <>
      <span aria-hidden className="grid size-10 shrink-0 place-items-center rounded-3 bg-bg-2 text-fg-2">
        {icon}
      </span>
      {/* Spans only: the card may be a button, which holds phrasing content. */}
      <span className="block min-w-0 flex-1">
        <span className="flex min-h-6 flex-wrap items-center gap-2">
          <span className="font-medium text-fg-1">{title}</span>
          {loading ? <Skeleton className="h-5 w-14 rounded-full" /> : !failed && status}
        </span>
        <span className="mt-1 block text-xs leading-relaxed text-fg-3">{desc}</span>
        {failed ? (
          <InlineError error={error} onRetry={onRetry} />
        ) : (
          (loading || detail != null || action != null) && (
            <span className="mt-2 flex min-h-6 items-center justify-between gap-3 text-sm">
              <span className="min-w-0 truncate text-fg-2 tabular-nums">{loading ? <Skeleton className="h-4 w-28" /> : detail}</span>
              {loading ? (
                <Skeleton className="h-4 w-12" />
              ) : (
                action != null && (
                  <span className={cn("inline-flex shrink-0 items-center gap-0.5 font-medium", emphasis ? "text-brand" : "text-fg-2")}>
                    {busy && <Spinner size={14} className="mr-1" />}
                    {action}
                    <ChevronRight size={16} aria-hidden />
                  </span>
                )
              )}
            </span>
          )
        )}
      </span>
    </>
  );
  const box = "flex w-full items-start gap-3 rounded-3 bg-bg-1 p-4 text-left";
  const pressable = cn(box, "transition-colors active:bg-bg-2 disabled:cursor-default");
  return (
    <motion.div variants={listItem} initial={entrance(index)} animate="animate" custom={index}>
      {failed ? (
        <div className={box}>{body}</div>
      ) : to !== undefined ? (
        <Link to={to} className={pressable}>
          {body}
        </Link>
      ) : (
        <button type="button" onClick={onClick} disabled={loading || busy} aria-busy={busy || undefined} className={pressable}>
          {body}
        </button>
      )}
    </motion.div>
  );
}

/** InlineError is a small failure with its retry, for one card's data. */
export function InlineError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation();
  return (
    <span role="alert" className="mt-1 flex items-center justify-between gap-2 text-sm text-danger">
      <span className="min-w-0 truncate">{errorText(error)}</span>
      {onRetry && (
        <TextButton tone="danger" onClick={onRetry} className="px-2">
          <RotateCcw size={14} /> {t("common.retry")}
        </TextButton>
      )}
    </span>
  );
}
