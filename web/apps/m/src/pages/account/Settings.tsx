import { routes, selectSignedIn, setLocale, useSession, useSettings, type UpDown } from "@exchange/core";
import { useAppEntry } from "@exchange/core/platform/apps";
import { browserTimeZone, zoneLabel, zoneOffset, zoneOptions } from "@exchange/core/user/preferences";
import { useProfile } from "@exchange/core/user/profile";
import { RadioGroup, Skeleton, Switch, TimeText, cn, useNow, type ComboboxItem } from "@exchange/ui";
import { LanguageSelect } from "@exchange/ui/components/LanguageSelect";
import { MyAvatar } from "@exchange/ui/profile/MyAvatar";
import { ChevronRight, Clock, Download } from "lucide-react";
import { useId, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { usePageHeader } from "../../layout/header";
import { PickerSheet } from "../auth/parts/PickerSheet";
import { Group, NavRow, Section } from "./parts/rows";
import { previewTones, type Tone } from "./parts/updown";

/**
 * Settings (design §7.2 我的 → 设置): signed in, the avatar and username
 * leading to the profile (design 2026-10-07, avatars and usernames §1 #5);
 * then language, time zone (a searchable list in a sheet), rise and fall
 * colours with a live preview, order confirmation and small balances. Kept
 * on this device (settings/store), so visitors may use it too.
 */
export default function Settings() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const upDown = useSettings((s) => s.upDown);
  const confirmOrders = useSettings((s) => s.confirmOrders);
  const hideSmall = useSettings((s) => s.hideSmallBalances);
  const set = useSettings((s) => s.set);
  const signedIn = useSession(selectSignedIn);
  const appEntry = useAppEntry();
  usePageHeader({ title: t("mAccount.settings.title"), back: routes.me }, [t]);

  return (
    <div className="flex flex-col gap-5 px-4 py-3">
      {signedIn && (
        <Group index={0}>
          <ProfileRow />
        </Group>
      )}
      <Section title={t("mAccount.settings.display")}>
        <Group index={0}>
          <div className="flex flex-col gap-3 p-4">
            <RowText label={t("mAccount.settings.language")} desc={t("mAccount.settings.languageDesc")} />
            {/* A dropdown of core's language list, each by its own and its English name (F30). */}
            <LanguageSelect value={locale} onValueChange={setLocale} size="lg" aria-label={t("mAccount.settings.language")} className="w-full" />
          </div>
          <TimeZoneRow />
        </Group>
      </Section>

      <Section title={t("mAccount.settings.market")}>
        <Group index={1}>
          <div className="flex flex-col gap-3 p-4">
            <RowText label={t("mAccount.settings.colors")} desc={t("mAccount.settings.colorsDesc")} />
            <RadioGroup
              variant="card"
              value={upDown}
              onValueChange={(v) => set({ upDown: v as UpDown })}
              aria-label={t("mAccount.settings.colors")}
              options={(["green-up", "red-up"] as const).map((o) => {
                const name = t(o === "green-up" ? "mAccount.settings.greenUp" : "mAccount.settings.redUp");
                return { value: o, label: name, description: <CandlePreview option={o} current={upDown} name={name} /> };
              })}
            />
          </div>
        </Group>
      </Section>

      <Section title={t("mAccount.settings.trading")}>
        <Group index={2}>
          <SwitchRow
            label={t("mAccount.settings.confirmOrders")}
            desc={t("mAccount.settings.confirmOrdersDesc")}
            checked={confirmOrders}
            onChange={(v) => set({ confirmOrders: v })}
          />
          <SwitchRow
            label={t("mAccount.settings.hideSmall")}
            desc={t("mAccount.settings.hideSmallDesc")}
            checked={hideSmall}
            onChange={(v) => set({ hideSmallBalances: v })}
          />
        </Group>
      </Section>

      {appEntry && (
        // The apps to download, as the console's switch says (design 2026-10-07, App download page §4, H6).
        <Group index={3}>
          <NavRow icon={<Download size={18} />} label={t("nav.downloadApp")} to={routes.download} />
        </Group>
      )}

      <p className="px-1 text-xs text-fg-3">{t("mAccount.settings.localNote")}</p>
    </div>
  );
}

// ProfileRow is the user's avatar and username, a way to the profile page.
function ProfileRow() {
  const { t } = useTranslation();
  const profile = useProfile();
  return (
    <Link to={routes.profile} data-testid="settings-profile" className="group flex min-h-16 items-center gap-3 px-4 py-3 transition-colors active:bg-bg-2">
      <MyAvatar size={40} decorative />
      <span className="min-w-0 flex-1">
        {profile.data ? (
          <span className="block truncate text-base font-medium text-fg-1">{profile.data.username}</span>
        ) : (
          <Skeleton className="h-5 w-32" />
        )}
        <span className="mt-0.5 block text-xs text-fg-3">{t("nav.profile")}</span>
      </span>
      <ChevronRight size={18} aria-hidden className="shrink-0 text-fg-3 transition-transform duration-[var(--t-fast)] group-active:translate-x-0.5" />
    </Link>
  );
}

function RowText({ label, desc }: { label: ReactNode; desc: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-base font-medium text-fg-1">{label}</div>
      <p className="mt-0.5 text-xs text-fg-3">{desc}</p>
    </div>
  );
}

/** SwitchRow is a whole-row target: the label forwards a tap anywhere on it to the switch. */
function SwitchRow({ label, desc, checked, onChange }: { label: ReactNode; desc: ReactNode; checked: boolean; onChange: (v: boolean) => void }) {
  const id = useId();
  return (
    <label htmlFor={id} className="flex min-h-16 cursor-pointer items-center justify-between gap-4 px-4 py-3 transition-colors active:bg-bg-2">
      <span className="min-w-0">
        <span className="block text-base font-medium text-fg-1">{label}</span>
        <span className="mt-0.5 block text-xs text-fg-3">{desc}</span>
      </span>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </label>
  );
}

function TimeZoneRow() {
  const { t } = useTranslation();
  const zone = useSettings((s) => s.timeZone);
  const set = useSettings((s) => s.set);
  const [open, setOpen] = useState(false);
  // The whole list (400-odd zones with their offsets) is built on first opening, then kept.
  const [opened, setOpened] = useState(false);
  const browser = browserTimeZone();
  const follow = t("mAccount.settings.followBrowser", { zone: zoneLabel(browser) || "—" });
  const items = useMemo<ComboboxItem[]>(() => {
    if (!opened) return [];
    const first: ComboboxItem = { value: "", label: follow, description: zoneOffset(browser), keywords: [browser] };
    return [first, ...zoneOptions().map((z) => ({ value: z.zone, label: zoneLabel(z.zone), description: z.offset, keywords: [z.zone, z.offset] }))];
  }, [opened, browser, follow]);
  return (
    <div className="flex flex-col gap-3 p-4">
      <RowText label={t("mAccount.settings.timeZone")} desc={t("mAccount.settings.timeZoneDesc")} />
      <button
        type="button"
        aria-haspopup="dialog"
        aria-label={`${t("mAccount.settings.timeZone")}: ${zone ? zoneLabel(zone) : follow}`}
        onClick={() => {
          setOpened(true);
          setOpen(true);
        }}
        className="flex h-tap w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-4 text-left transition-colors active:bg-bg-3"
      >
        <span className="min-w-0 flex-1 truncate text-md text-fg-1">{zone ? zoneLabel(zone) : follow}</span>
        <span className="shrink-0 text-xs text-fg-3 tabular-nums">{zoneOffset(zone || browser)}</span>
        <ChevronRight size={18} className="shrink-0 text-fg-3" aria-hidden />
      </button>
      <LiveClock />
      <PickerSheet
        open={open}
        onOpenChange={setOpen}
        title={t("mAccount.settings.pickZone")}
        items={items}
        value={zone}
        onChoose={(v) => set({ timeZone: v })}
        searchPlaceholder={t("mAccount.settings.searchZone")}
      />
    </div>
  );
}

function LiveClock() {
  const { t } = useTranslation();
  const now = useNow(1000);
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-fg-3">
      <Clock size={12} aria-hidden />
      {t("mAccount.settings.nowIn")}
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
// The preview sits on the chosen card's brand tint: soft-fg shades read there.
const text: Record<Tone, string> = { up: "text-up-soft-fg", down: "text-down-soft-fg" };

/** CandlePreview draws an option's rises and falls in the colours it would use (updown.ts). */
function CandlePreview({ option, current, name }: { option: UpDown; current: UpDown; name: string }) {
  const { t } = useTranslation();
  const tones = previewTones(option, current);
  // Narrow enough for a 360 px screen: the card leaves about 248 px inside.
  const w = 144;
  const h = 56;
  const y = (v: number) => h - 4 - ((v - 22) / 50) * (h - 8);
  const step = w / CANDLES.length;
  return (
    <span className="mt-2 flex items-center gap-3">
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={t("mAccount.settings.previewLabel", { name })} className="shrink-0">
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
        <span className={cn("rounded-1 py-0.5", text[tones.rise])}>{t("mAccount.settings.rise")} +2.35%</span>
        <span className={cn("rounded-1 py-0.5", text[tones.fall])}>{t("mAccount.settings.fall")} −1.18%</span>
      </span>
    </span>
  );
}
