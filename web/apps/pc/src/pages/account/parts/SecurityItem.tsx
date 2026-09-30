import { errorText } from "@exchange/core";
import { Button, listItem, Skeleton, cn } from "@exchange/ui";
import { RotateCcw } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type SecurityItemProps = {
  icon: ReactNode;
  title: ReactNode;
  desc: ReactNode;
  /** The status badge next to the title. */
  status?: ReactNode;
  /** A value shown before the actions (a masked email, a count). */
  detail?: ReactNode;
  action?: ReactNode;
  /** Its place in the list, for the first-screen stagger. */
  index: number;
  /** The item's data is loading: skeletons stand in for status and actions. */
  loading?: boolean;
  /** The item's data failed: its text and a retry stand in for them. */
  error?: unknown;
  onRetry?: () => void;
  id?: string;
};

/**
 * SecurityItem is one card of the security centre: an icon, a title with
 * its status, what it is for, the current value and the actions. Cards
 * lift a little on hover.
 */
export function SecurityItem({ icon, title, desc, status, detail, action, index, loading, error, onRetry, id }: SecurityItemProps) {
  return (
    <motion.div
      id={id}
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      className="group flex items-center gap-4 rounded-3 border border-line-1 bg-bg-1 p-4 transition-[translate,border-color] duration-[var(--t-base)] hover:-translate-y-0.5 hover:border-line-2"
    >
      <span className="grid size-11 shrink-0 place-items-center rounded-3 bg-bg-2 text-fg-2 transition-colors group-hover:text-brand">{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-fg-1">{title}</span>
          {loading ? <Skeleton className="h-5 w-14 rounded-full" /> : error == null && status}
        </div>
        <p className="mt-0.5 text-sm leading-relaxed text-fg-3">{desc}</p>
      </div>
      {error != null ? (
        <InlineError error={error} onRetry={onRetry} />
      ) : loading ? (
        <Skeleton className="h-8 w-20" />
      ) : (
        <>
          {detail !== undefined && detail !== null && <div className="shrink-0 text-right text-sm text-fg-2 tabular-nums">{detail}</div>}
          {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
        </>
      )}
    </motion.div>
  );
}

/** InlineError is a small failure with its retry, for one card's data. */
export function InlineError({ error, onRetry, className }: { error: unknown; onRetry?: () => void; className?: string }) {
  const { t } = useTranslation();
  return (
    <div role="alert" className={cn("flex shrink-0 items-center gap-2 text-sm text-danger", className)}>
      <span className="max-w-56 truncate" title={errorText(error)}>
        {errorText(error)}
      </span>
      {onRetry && (
        <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} onClick={onRetry}>
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}
