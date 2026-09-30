import { cn } from "@exchange/ui";
import type { ReactNode } from "react";
import { Link, type To } from "react-router";

// Text actions ("忘记密码？", "立即注册", "更换") at the size of a finger:
// at least 44 px tall (design §7.1), whatever the text's size.

type Tone = "brand" | "muted" | "danger";

const tones: Record<Tone, string> = { brand: "text-brand", muted: "text-fg-2", danger: "text-danger" };

const base = "inline-flex min-h-11 shrink-0 items-center gap-1 text-sm font-medium transition-opacity active:opacity-60 disabled:opacity-50";

/** TextLink is an in-site link that reads as text. */
export function TextLink({
  to, state, replace, tone = "brand", className, children,
}: {
  to: To;
  state?: unknown;
  replace?: boolean;
  tone?: Tone;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Link to={to} state={state} replace={replace} className={cn(base, tones[tone], className)}>
      {children}
    </Link>
  );
}

/** TextButton is a button that reads as text. */
export function TextButton({
  onClick, tone = "brand", disabled, className, children,
}: {
  onClick: () => void;
  tone?: Tone;
  disabled?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} className={cn(base, tones[tone], className)}>
      {children}
    </button>
  );
}
