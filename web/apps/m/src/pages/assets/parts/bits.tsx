import { shortAddress } from "@exchange/core/wallet/networks";
import { Button, Skeleton, cn, copyText, listItem } from "@exchange/ui";
import { Check, Copy } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

// Small building blocks of the mobile assets pages: cards, 44 px text and
// copy buttons, list skeletons and the shared press feedback.

/** Press feedback of tappable cards and rows (transform only, design §5.3). */
export const PRESS = "transition-transform duration-[var(--t-fast)] ease-out active:scale-[0.98]";

/** Gives the shared ErrorState's retry button a 44 px touch target. */
export const RETRY = "[&_button]:h-11 [&_button]:px-5";

/** Only the first screen of a list animates in (design §5.3: 20 ms apart, at most 12 rows). */
export const STAGGERED = 12;

/** Appear is a list item that fades in with the first-screen stagger (later rows just show). */
export function Appear({ index, children, className }: { index: number; children: ReactNode; className?: string }) {
  if (index >= STAGGERED) return <li className={className}>{children}</li>;
  return (
    <motion.li variants={listItem} initial="initial" animate="animate" custom={index} className={className}>
      {children}
    </motion.li>
  );
}

/**
 * useKept returns the value, or while it is null the last value it had:
 * a sheet that is closing keeps its content during its exit animation.
 * Pass values with a stable identity (state, cached records).
 */
export function useKept<T>(value: T | null): T | null {
  const [kept, setKept] = useState<T | null>(value);
  if (value !== null && value !== kept) setKept(value);
  return value ?? kept;
}

/** Section is a card of a page: an optional title row, then its content. */
export function Section({
  title, extra, children, className, bodyClassName, id,
}: { title?: ReactNode; extra?: ReactNode; children: ReactNode; className?: string; bodyClassName?: string; id?: string }) {
  const head = Boolean(title || extra);
  return (
    <section id={id} className={cn("rounded-3 bg-bg-1", className)}>
      {head && (
        <div className="flex min-h-11 items-center justify-between gap-3 px-4 pt-1">
          {title ? <h2 className="text-md font-semibold text-fg-1">{title}</h2> : <span />}
          {extra ? <div className="flex items-center">{extra}</div> : null}
        </div>
      )}
      <div className={cn("p-4", head && "pt-2", bodyClassName)}>{children}</div>
    </section>
  );
}

/** TextButton is a link-looking button with a 44 px touch target ("更换", "全部"). */
export function TextButton({ className, children, type = "button", ...rest }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type={type}
      {...rest}
      className={cn(
        "inline-flex min-h-11 shrink-0 items-center justify-center px-3 text-sm font-medium text-brand transition-opacity active:opacity-70 disabled:opacity-40",
        className,
      )}
    >
      {children}
    </button>
  );
}

/** CopyIcon copies a value from a 44 px button and shows a check for 1.5 s. */
export function CopyIcon({ value, label, className }: { value: string; label?: string; className?: string }) {
  const { t } = useTranslation();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (!(await copyText(value))) return;
    setDone(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setDone(false), 1500);
  };
  return (
    <button
      type="button"
      onClick={() => void copy()}
      aria-label={done ? t("common.copied") : (label ?? t("common.copy"))}
      className={cn("grid size-11 shrink-0 place-items-center rounded-2 text-fg-3 active:bg-bg-2", done && "text-success", className)}
    >
      {done ? <Check size={16} className="animate-fade-in" /> : <Copy size={16} />}
      <span aria-live="polite" className="sr-only">
        {done ? t("common.copied") : ""}
      </span>
    </button>
  );
}

/** AddressValue is a long address or hash, shortened, with a 44 px copy button. */
export function AddressValue({ address, head = 10, tail = 8 }: { address: string; head?: number; tail?: number }) {
  return (
    <span className="-my-2.5 inline-flex items-center">
      <span className="font-mono text-xs text-fg-2">{shortAddress(address, head, tail)}</span>
      <CopyIcon value={address} className="-mr-3" />
    </span>
  );
}

/** CopyAction is the big "copy" button of a page (the deposit address). */
export function CopyAction({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (!(await copyText(value))) return;
    setDone(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setDone(false), 1500);
  };
  return (
    <Button size="lg" block icon={done ? <Check size={18} className="animate-fade-in" /> : <Copy size={18} />} onClick={() => void copy()}>
      {done ? t("common.copied") : label}
      <span aria-live="polite" className="sr-only">
        {done ? t("common.copied") : ""}
      </span>
    </Button>
  );
}

/** CardSkeleton holds the place of record cards while they load. */
export function CardSkeleton({ rows = 3, tall }: { rows?: number; tall?: boolean }) {
  return (
    <ul className="flex flex-col gap-2" aria-hidden>
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} className="rounded-3 bg-bg-1 p-3">
          <div className="flex items-center gap-3">
            <Skeleton round className="size-8" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3 w-24" />
            </div>
            <Skeleton className="h-5 w-14 rounded-full" />
          </div>
          {tall && <Skeleton className="mt-3 h-6 w-full" />}
        </li>
      ))}
    </ul>
  );
}

/** LoadMore asks for the next page of a list. */
export function LoadMore({ loading, onClick }: { loading: boolean; onClick: () => void }) {
  const { t } = useTranslation();
  return (
    <Button variant="ghost" size="lg" block loading={loading} onClick={onClick}>
      {t("mAssets.common.loadMore")}
    </Button>
  );
}
