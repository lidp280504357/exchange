import type { OtpChannel } from "@exchange/core/auth/otp";
import { Check, Mail, Smartphone } from "lucide-react";
import { RadioGroup as RRadio } from "radix-ui";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

/** A way a code can go: its channel, the masked email or phone when known, and whether the account has it. */
export type ChannelOption = { channel: OtpChannel; target?: string; bound: boolean };

export type ChannelCardsProps = {
  options: readonly ChannelOption[];
  value: OtpChannel;
  onValueChange: (channel: OtpChannel) => void;
  /** lg: the phone's taller cards. */
  size?: "md" | "lg";
  className?: string;
};

const ICONS = { EMAIL: Mail, SMS: Smartphone } as const;

/**
 * ChannelCards picks where a verification code goes (F32): a card per
 * channel, side by side and equally wide, each with its icon, its name and
 * the masked email or phone; the chosen one outlined in the brand colour
 * with a check, one the account lacks greyed and marked 未绑定. With a
 * single channel there is nothing to pick: one line says where the code
 * goes. Arrow keys move between the cards (a radio group).
 */
export function ChannelCards({ options, value, onValueChange, size = "md", className }: ChannelCardsProps) {
  const { t } = useTranslation();
  const where = (o: ChannelOption) => o.target ?? t(`ui.otpChannel.yours.${o.channel}`);
  if (options.length === 1) {
    const [only] = options as [ChannelOption];
    const Icon = ICONS[only.channel];
    return (
      <p data-testid="otp-channel-only" className={cn("flex min-w-0 items-center gap-2 text-sm text-fg-2", className)}>
        <Icon size={16} className="shrink-0 text-fg-3" />
        <span className="truncate">{t("ui.otpChannel.only", { target: where(only) })}</span>
      </p>
    );
  }
  return (
    <RRadio.Root
      value={value}
      onValueChange={(v) => onValueChange(v as OtpChannel)}
      orientation="horizontal"
      aria-label={t("ui.otpChannel.label")}
      className={cn("grid auto-cols-fr grid-flow-col gap-2", className)}
    >
      {options.map((o) => {
        const Icon = ICONS[o.channel];
        return (
          <RRadio.Item
            key={o.channel}
            value={o.channel}
            disabled={!o.bound}
            data-channel={o.channel}
            className={cn(
              "group relative flex min-w-0 flex-col items-start gap-1 rounded-2 border border-line-1 bg-bg-2 px-3 text-left outline-none transition-colors",
              "hover:border-line-2 focus-visible:ring-1 focus-visible:ring-brand",
              "data-[state=checked]:border-brand data-[state=checked]:bg-brand-soft",
              "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:border-line-1",
              size === "lg" ? "py-3" : "py-2.5",
            )}
          >
            <span className="flex items-center gap-1.5 pr-5 text-sm font-medium text-fg-1">
              <Icon size={16} className="shrink-0 text-fg-2 group-data-[state=checked]:text-brand" />
              {t(`ui.otpChannel.name.${o.channel}`)}
            </span>
            {/* fg-2: a checked card's brand tint takes fg-3 under 4.5:1. */}
            <span className="w-full truncate text-xs text-fg-2 tabular-nums">{o.bound ? where(o) : t("ui.otpChannel.unbound")}</span>
            <RRadio.Indicator className="absolute right-2 top-2 grid size-4 animate-fade-in place-items-center rounded-full bg-brand text-brand-fg">
              <Check size={11} strokeWidth={3} />
            </RRadio.Indicator>
          </RRadio.Item>
        );
      })}
    </RRadio.Root>
  );
}
