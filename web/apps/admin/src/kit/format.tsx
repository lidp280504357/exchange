import { dec, formatDecimal, formatTime, timeZoneOf, useSettings, type TimeStyle } from "@exchange/core";
import { cn, CopyButton } from "@exchange/ui";
import type { ReactNode } from "react";
import { useNavigate } from "react-router";

// How the console shows values (design §10.2): amounts as exact decimal
// strings with thousands separators, times in the administrator's time
// zone (settings), IDs shortened with a copy button and the whole ID on
// hover; a user ID leads to the user's page.

/** useTimeText formats times in the administrator's locale and time zone. */
export function useTimeText() {
  const locale = useSettings((s) => s.locale);
  const zone = useSettings((s) => timeZoneOf(s));
  return (t: string | null | undefined, style: TimeStyle = "datetimeSeconds") => (t ? formatTime(t, style, locale, zone) : "—");
}

export function TimeText({ value, style }: { value: string | null | undefined; style?: TimeStyle }) {
  const time = useTimeText();
  return <span className="whitespace-nowrap tabular-nums">{time(value, style)}</span>;
}

export type NumProps = {
  value: string | null | undefined;
  /** Decimal places (rounded down); the value's own by default. */
  decimals?: number;
  /** An asset after the number. */
  unit?: ReactNode;
  /** Color by sign: gains up, losses down. */
  signed?: boolean;
  className?: string;
};

/** Num shows a decimal string exactly, grouped, right-aligned digits. */
export function Num({ value, decimals, unit, signed, className }: NumProps) {
  const text = formatDecimal(value, { decimals, sign: signed });
  const sign = signed && value && dec.isDecimal(value) ? dec.sign(value) : 0;
  return (
    <span className={cn("whitespace-nowrap font-mono tabular-nums", sign > 0 && "text-up-strong", sign < 0 && "text-down-strong", className)}>
      {text}
      {unit && text !== "—" && <span className="ml-1 font-sans text-fg-3">{unit}</span>}
    </span>
  );
}

/** IdText shows the start of an ID with the whole of it on hover and a copy button. */
export function IdText({ value, chars = 8, className }: { value: string | null | undefined; chars?: number; className?: string }) {
  if (!value) return <span className="text-fg-3">—</span>;
  const short = value.length > chars + 2 ? `${value.slice(0, chars)}…` : value;
  return (
    <span className={cn("inline-flex items-center gap-1 font-mono text-xs", className)} title={value}>
      <span>{short}</span>
      <CopyButton value={value} size={12} />
    </span>
  );
}

/** useOpenUser opens a user's page (/users/<id>). */
export function useOpenUser() {
  const navigate = useNavigate();
  return (id: string) => navigate(`/users/${id}`);
}

/** UserCell is a user ID that opens the user's page. */
export function UserCell({ id }: { id: string | null | undefined }) {
  const open = useOpenUser();
  if (!id) return <span className="text-fg-3">—</span>;
  return (
    <span className="inline-flex items-center gap-1 font-mono text-xs">
      <button
        type="button"
        title={id}
        className="text-info-strong hover:underline"
        onClick={(e) => {
          e.stopPropagation();
          open(id);
        }}
      >
        {id.slice(0, 8)}…
      </button>
      <CopyButton value={id} size={12} />
    </span>
  );
}
