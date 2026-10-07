import { can, type Admin, type Permission } from "@exchange/core/api/admin";
import {
  Activity,
  ArrowDownToLine,
  ArrowUpFromLine,
  BellRing,
  BookOpen,
  BookOpenText,
  Bot,
  Building2,
  CalendarClock,
  ChartColumn,
  ChartLine,
  FileText,
  Gauge,
  Gem,
  Flame,
  HandCoins,
  HeartPulse,
  Coins,
  KeyRound,
  Landmark,
  Layers,
  LayoutDashboard,
  ListOrdered,
  Megaphone,
  Percent,
  Rocket,
  ScrollText,
  Settings,
  ShieldAlert,
  Siren,
  SlidersHorizontal,
  Smartphone,
  SlidersVertical,
  Stamp,
  UserCog,
  UserCheck,
  Users,
  Warehouse,
  type LucideIcon,
} from "lucide-react";
import { useFlagCheck } from "./kit/flags";
import { pages, type Page } from "./pageLoaders";

/** The sidebar's groups (design 2026-10-02 §3); a group with one section shows as that section. */
export type GroupKey = "overview" | "users" | "funds" | "trading" | "margin" | "markets" | "sim" | "risk" | "ops" | "system";

export const groups: GroupKey[] = ["overview", "users", "funds", "trading", "margin", "markets", "sim", "risk", "ops", "system"];

export type Section = {
  path: string;
  key: string;
  group: GroupKey;
  /** Shown with any of these permissions; every administrator without. */
  perm?: Permission | Permission[];
  icon: LucideIcon;
  /** Its page (pageLoaders.tsx). */
  page: Page;
  /** Shown only while this feature flag is on (kit/flags.ts). */
  flag?: string;
};

/** The console's sections; a section hides without its permission. */
export const sections: Section[] = [
  { path: "", key: "overview", group: "overview", perm: "reports.read", icon: LayoutDashboard, page: pages[""] },
  { path: "users", key: "users", group: "users", perm: "users.read", icon: Users, page: pages.users },
  {
    path: "identity-requests", key: "identityRequests", group: "users", perm: "users.read", icon: UserCheck,
    page: pages["identity-requests"],
  },
  { path: "deposits", key: "deposits", group: "funds", perm: "withdrawals.read", icon: ArrowDownToLine, page: pages.deposits },
  { path: "withdrawals", key: "withdrawals", group: "funds", perm: "withdrawals.read", icon: ArrowUpFromLine, page: pages.withdrawals },
  { path: "custody", key: "custody", group: "funds", perm: "withdrawals.read", icon: Landmark, page: pages.custody },
  {
    path: "adjustments", key: "adjustments", group: "funds", perm: "ledger.adjust.request", icon: SlidersHorizontal,
    page: pages.adjustments,
  },
  {
    path: "approvals", key: "approvals", group: "funds", perm: ["ledger.adjust.request", "audit.read"], icon: Stamp,
    page: pages.approvals,
  },
  { path: "ledger", key: "ledger", group: "funds", perm: "reports.read", icon: BookOpen, page: pages.ledger },
  { path: "orders", key: "orders", group: "trading", perm: "reports.read", icon: ListOrdered, page: pages.orders },
  { path: "positions", key: "positions", group: "trading", perm: "derivatives.read", icon: Layers, page: pages.positions },
  {
    path: "liquidations", key: "liquidations", group: "trading", perm: "derivatives.read", icon: Siren,
    page: pages.liquidations,
  },
  { path: "derivatives", key: "derivatives", group: "trading", perm: "derivatives.read", icon: ChartLine, page: pages.derivatives },
  { path: "house", key: "house", group: "trading", perm: "reports.read", icon: Warehouse, page: pages.house },
  // Margin trading (design 2026-10-06 §8, E5): behind margin.enabled; the
  // pages show sample data until the API lands (A55).
  {
    path: "margin/accounts", key: "marginAccounts", group: "margin", perm: "derivatives.read", icon: HandCoins,
    page: pages["margin/accounts"], flag: "margin.enabled",
  },
  {
    path: "margin/liquidations", key: "marginLiquidations", group: "margin", perm: "reports.read", icon: Flame,
    page: pages["margin/liquidations"], flag: "margin.enabled",
  },
  {
    path: "margin/interest", key: "marginInterest", group: "margin", perm: "reports.read", icon: Percent,
    page: pages["margin/interest"], flag: "margin.enabled",
  },
  {
    path: "margin/params", key: "marginParams", group: "margin", perm: "instruments.read", icon: SlidersVertical,
    page: pages["margin/params"], flag: "margin.enabled",
  },
  { path: "instruments", key: "instruments", group: "markets", perm: "instruments.read", icon: Coins, page: pages.instruments },
  { path: "sim", key: "simOverview", group: "sim", perm: "reports.read", icon: Activity, page: pages.sim },
  { path: "sim/control", key: "simControl", group: "sim", perm: "reports.read", icon: Gauge, page: pages["sim/control"] },
  { path: "sim/events", key: "simEvents", group: "sim", perm: "reports.read", icon: CalendarClock, page: pages["sim/events"] },
  { path: "sim/bots", key: "simBots", group: "sim", perm: "reports.read", icon: Bot, page: pages["sim/bots"] },
  { path: "sim/token", key: "simToken", group: "sim", perm: "reports.read", icon: Gem, page: pages["sim/token"] },
  { path: "risk", key: "risk", group: "risk", perm: "flags.read", icon: ShieldAlert, page: pages.risk },
  { path: "announcements", key: "announcements", group: "ops", icon: Megaphone, page: pages.announcements },
  { path: "help-articles", key: "helpArticles", group: "ops", icon: BookOpenText, page: pages["help-articles"] },
  { path: "pages", key: "fixedPages", group: "ops", icon: FileText, page: pages.pages },
  { path: "broadcasts", key: "broadcasts", group: "ops", icon: BellRing, page: pages.broadcasts },
  { path: "admins", key: "admins", group: "system", perm: "admins.manage", icon: UserCog, page: pages.admins },
  { path: "audit", key: "audit", group: "system", perm: "audit.read", icon: ScrollText, page: pages.audit },
  { path: "reports", key: "reports", group: "system", perm: "reports.read", icon: ChartColumn, page: pages.reports },
  { path: "health", key: "health", group: "system", perm: "reports.read", icon: HeartPulse, page: pages.health },
  { path: "platform", key: "platform", group: "system", perm: "reports.read", icon: Building2, page: pages.platform },
  // The apps to download (design 2026-10-07, App download page; H2).
  { path: "platform/apps", key: "appDownloads", group: "system", perm: "reports.read", icon: Smartphone, page: pages["platform/apps"] },
  { path: "launch", key: "launch", group: "system", perm: "reports.read", icon: Rocket, page: pages.launch },
  { path: "settings", key: "settings", group: "system", icon: Settings, page: pages.settings },
];

/** Pages under a section that the sidebar does not list (a record's own page). */
export const subpages: Section[] = [
  { path: "users/:id", key: "users", group: "users", perm: "users.read", icon: Users, page: pages["users/:id"] },
  // One's own password and authenticator, from the administrator's menu (C5.5 ⑪).
  { path: "account", key: "account", group: "system", icon: KeyRound, page: pages.account },
];

/** allowed reports whether an administrator may open a section. */
export function allowed(admin: Admin, s: Section): boolean {
  if (!s.perm) return true;
  return (Array.isArray(s.perm) ? s.perm : [s.perm]).some((p) => can(admin, p));
}

/** useFlagged returns whether a section's flag lets it show: true without one, undefined while the flags load. */
export function useFlagged(): (s: Section) => boolean | undefined {
  const on = useFlagCheck();
  return (s) => (s.flag ? on(s.flag) : true);
}
