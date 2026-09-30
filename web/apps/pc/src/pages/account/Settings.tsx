import { setLocale, useSettings, type Locale, type UpDown } from "@exchange/core";
import { browserTimeZone, zoneLabel, zoneOffset, zoneOptions } from "@exchange/core/user/preferences";
import { Combobox, RadioGroup, Switch, TimeText, listItem, useNow, cn, type ComboboxItem } from "@exchange/ui";
import { ChartCandlestick, Clock, Languages, SlidersHorizontal } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AccountLayout } from "./parts/AccountLayout";
import { previewTones, type Tone } from "./parts/updown";

/**
 * Settings (design §6.2 账户): language, time zone, rise and fall colours
 * with a live preview, order confirmation and small balances. Kept in
 * this browser (settings/store); there is no notification-preference API
 * yet, so notifications are not configurable here.
 */
export default function Settings() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const upDown = useSettings((s) => s.upDown);
  const confirmOrders = useSettings((s) => s.confirmOrders);
  const hideSmall = useSettings((s) => s.hideSmallBalances);
  const set = useSettings((s) => s.set);

  return (
    <AccountLayout title={t("pcAccount.settings.title")} subtitle={t("pcAccount.settings.subtitle")}>
      <Card index={0} icon={<Languages size={18} />} title={t("pcAccount.settings.display")}>
        <Row label={t("pcAccount.settings.language")} desc={t("pcAccount.settings.languageDesc")}>
          <RadioGroup
            orientation="horizontal"
            variant="card"
            value={locale}
            onValueChange={(v) => setLocale(v as Locale)}
            aria-label={t("pcAccount.settings.language")}
            className="[&>label]:min-w-40"
            options={(["zh-CN", "en"] as const).map((l) => ({
              value: l,
              label: t(`pcAccount.settings.langNames.${l}`),
              description: t(`pcAccount.settings.langDesc.${l}`),
            }))}
          />
        </Row>
        <Row label={t("pcAccount.settings.timeZone")} desc={t("pcAccount.settings.timeZoneDesc")}>
          <div className="flex flex-col items-end gap-2">
            <TimeZonePicker />
            <LiveClock />
          </div>
        </Row>
      </Card>

      <Card index={1} icon={<ChartCandlestick size={18} />} title={t("pcAccount.settings.market")}>
        <div className="flex flex-col gap-3 py-4">
          <div>
            <div className="text-sm font-medium text-fg-1">{t("pcAccount.settings.colors")}</div>
            <p className="mt-0.5 text-xs text-fg-3">{t("pcAccount.settings.colorsDesc")}</p>
          </div>
          <RadioGroup
            orientation="horizontal"
            variant="card"
            value={upDown}
            onValueChange={(v) => set({ upDown: v as UpDown })}
            aria-label={t("pcAccount.settings.colors")}
            className="[&>label]:min-w-64 [&>label]:flex-1"
            options={(["green-up", "red-up"] as const).map((o) => {
              const name = t(o === "green-up" ? "pcAccount.settings.greenUp" : "pcAccount.settings.redUp");
              return { value: o, label: name, description: <CandlePreview option={o} current={upDown} name={name} /> };
            })}
          />
        </div>
      </Card>

      <Card index={2} icon={<SlidersHorizontal size={18} />} title={t("pcAccount.settings.trading")}>
        <div className="py-4">
          <Switch
            labelFirst
            checked={confirmOrders}
            onCheckedChange={(v) => set({ confirmOrders: v })}
            label={t("pcAccount.settings.confirmOrders")}
            description={t("pcAccount.settings.confirmOrdersDesc")}
          />
        </div>
        <div className="py-4">
          <Switch
            labelFirst
            checked={hideSmall}
            onCheckedChange={(v) => set({ hideSmallBalances: v })}
            label={t("pcAccount.settings.hideSmall")}
            description={t("pcAccount.settings.hideSmallDesc")}
          />
        </div>
      </Card>
    </AccountLayout>
  );
}

function Card({ index, icon, title, children }: { index: number; icon: ReactNode; title: ReactNode; children: ReactNode }) {
  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      className="rounded-3 border border-line-1 bg-bg-1 px-5 pt-4"
    >
      <h2 className="flex items-center gap-2 border-b border-line-1 pb-3 text-md font-semibold text-fg-1">
        <span className="grid size-8 place-items-center rounded-2 bg-bg-2 text-brand">{icon}</span>
        {title}
      </h2>
      <div className="flex flex-col divide-y divide-line-1">{children}</div>
    </motion.section>
  );
}

function Row({ label, desc, children }: { label: ReactNode; desc: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4 py-4">
      <div className="min-w-0">
        <div className="text-sm font-medium text-fg-1">{label}</div>
        <p className="mt-0.5 text-xs text-fg-3">{desc}</p>
      </div>
      {children}
    </div>
  );
}

function TimeZonePicker() {
  const { t } = useTranslation();
  const zone = useSettings((s) => s.timeZone);
  const set = useSettings((s) => s.set);
  const [open, setOpen] = useState(false);
  const items = useMemo<ComboboxItem[]>(() => {
    const browser = browserTimeZone();
    const follow: ComboboxItem = {
      value: "",
      label: t("pcAccount.settings.followBrowser", { zone: zoneLabel(browser) || "—" }),
      description: zoneOffset(browser),
    };
    // The whole list (400-odd zones with their offsets) only while open.
    const list = open ? zoneOptions() : zone ? [{ zone, offset: zoneOffset(zone) }] : [];
    return [follow, ...list.map((z) => ({ value: z.zone, label: zoneLabel(z.zone), description: z.offset, keywords: [z.zone, z.offset] }))];
  }, [open, zone, t]);
  return (
    <Combobox
      items={items}
      value={zone}
      onValueChange={(v) => set({ timeZone: v })}
      open={open}
      onOpenChange={setOpen}
      align="end"
      className="w-80"
      searchPlaceholder={t("pcAccount.settings.searchZone")}
      aria-label={t("pcAccount.settings.timeZone")}
    />
  );
}

function LiveClock() {
  const { t } = useTranslation();
  const now = useNow(1000);
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-fg-3">
      <Clock size={12} />
      {t("pcAccount.settings.nowIn")}
      <TimeText value={now} format="datetimeSeconds" className="text-fg-2" />
    </span>
  );
}

// A made-up run of candles for the preview: open, close, high, low.
const CANDLES: [number, number, number, number][] = [
  [30, 42, 46, 26], [42, 36, 45, 33], [36, 48, 52, 34], [48, 44, 50, 40], [44, 56, 58, 42], [56, 50, 59, 47], [50, 60, 63, 49], [60, 66, 70, 57],
];

const fill: Record<Tone, string> = { up: "fill-up", down: "fill-down" };
const stroke: Record<Tone, string> = { up: "stroke-up", down: "stroke-down" };
const text: Record<Tone, string> = { up: "text-up", down: "text-down" };

/** CandlePreview draws an option's rises and falls in the colours it would use (updown.ts). */
function CandlePreview({ option, current, name }: { option: UpDown; current: UpDown; name: string }) {
  const { t } = useTranslation();
  const tones = previewTones(option, current);
  const w = 176;
  const h = 64;
  const y = (v: number) => h - 4 - ((v - 22) / 50) * (h - 8);
  const step = w / CANDLES.length;
  return (
    <span className="mt-2 flex items-center gap-4">
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={t("pcAccount.settings.previewLabel", { name })} className="shrink-0">
        {CANDLES.map(([o, c, hi, lo], i) => {
          const tone = c >= o ? tones.rise : tones.fall;
          const x = i * step + step / 2;
          return (
            <g key={i}>
              <line x1={x} x2={x} y1={y(hi)} y2={y(lo)} strokeWidth={1.5} className={stroke[tone]} />
              <rect x={x - step * 0.28} width={step * 0.56} y={y(Math.max(o, c))} height={Math.max(2, Math.abs(y(o) - y(c)))} rx={1} className={fill[tone]} />
            </g>
          );
        })}
      </svg>
      <span className="flex flex-col gap-1.5 text-xs font-medium tabular-nums">
        <span className={cn("rounded-1 px-1.5 py-0.5", text[tones.rise])}>
          {t("pcAccount.settings.rise")} +2.35%
        </span>
        <span className={cn("rounded-1 px-1.5 py-0.5", text[tones.fall])}>
          {t("pcAccount.settings.fall")} −1.18%
        </span>
      </span>
    </span>
  );
}
