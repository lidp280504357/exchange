import { can, type Admin, type Permission } from "@exchange/core/api/admin";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  BookOpen,
  ChartColumn,
  ChartLine,
  Coins,
  Landmark,
  Layers,
  LayoutDashboard,
  ListOrdered,
  ScrollText,
  Settings,
  ShieldAlert,
  Siren,
  SlidersHorizontal,
  Stamp,
  UserCheck,
  Users,
  Warehouse,
  type LucideIcon,
} from "lucide-react";
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

/** The sidebar's groups (design 2026-10-02 §3); a group with one section shows as that section. */
export type GroupKey = "overview" | "users" | "funds" | "trading" | "markets" | "risk" | "ops" | "system";

export const groups: GroupKey[] = ["overview", "users", "funds", "trading", "markets", "risk", "ops", "system"];

export type Section = {
  path: string;
  key: string;
  group: GroupKey;
  /** Shown with any of these permissions; every administrator without. */
  perm?: Permission | Permission[];
  icon: LucideIcon;
  page: LazyExoticComponent<ComponentType<{ admin: Admin }>>;
};

/** The console's sections; a section hides without its permission. */
export const sections: Section[] = [
  { path: "", key: "overview", group: "overview", perm: "reports.read", icon: LayoutDashboard, page: lazy(() => import("./pages/Overview")) },
  { path: "users", key: "users", group: "users", perm: "users.read", icon: Users, page: lazy(() => import("./pages/users/Users")) },
  {
    path: "identity-requests", key: "identityRequests", group: "users", perm: "users.read", icon: UserCheck,
    page: lazy(() => import("./pages/users/IdentityRequests")),
  },
  { path: "deposits", key: "deposits", group: "funds", perm: "withdrawals.read", icon: ArrowDownToLine, page: lazy(() => import("./pages/wallet/Deposits")) },
  { path: "withdrawals", key: "withdrawals", group: "funds", perm: "withdrawals.read", icon: ArrowUpFromLine, page: lazy(() => import("./pages/wallet/Withdrawals")) },
  { path: "custody", key: "custody", group: "funds", perm: "withdrawals.read", icon: Landmark, page: lazy(() => import("./pages/wallet/Custody")) },
  {
    path: "adjustments", key: "adjustments", group: "funds", perm: "ledger.adjust.request", icon: SlidersHorizontal,
    page: lazy(() => import("./pages/funds/Adjustments")),
  },
  {
    path: "approvals", key: "approvals", group: "funds", perm: ["ledger.adjust.request", "audit.read"], icon: Stamp,
    page: lazy(() => import("./pages/funds/Approvals")),
  },
  { path: "ledger", key: "ledger", group: "funds", perm: "reports.read", icon: BookOpen, page: lazy(() => import("./pages/Ledger")) },
  { path: "orders", key: "orders", group: "trading", perm: "reports.read", icon: ListOrdered, page: lazy(() => import("./pages/orders/Orders")) },
  { path: "positions", key: "positions", group: "trading", perm: "derivatives.read", icon: Layers, page: lazy(() => import("./pages/trading/Positions")) },
  {
    path: "liquidations", key: "liquidations", group: "trading", perm: "derivatives.read", icon: Siren,
    page: lazy(() => import("./pages/trading/Liquidations")),
  },
  { path: "derivatives", key: "derivatives", group: "trading", perm: "derivatives.read", icon: ChartLine, page: lazy(() => import("./pages/Derivatives")) },
  { path: "house", key: "house", group: "trading", perm: "reports.read", icon: Warehouse, page: lazy(() => import("./pages/House")) },
  { path: "instruments", key: "instruments", group: "markets", perm: "instruments.read", icon: Coins, page: lazy(() => import("./pages/Instruments")) },
  { path: "risk", key: "risk", group: "risk", perm: "flags.read", icon: ShieldAlert, page: lazy(() => import("./pages/Flags")) },
  { path: "audit", key: "audit", group: "system", perm: "audit.read", icon: ScrollText, page: lazy(() => import("./pages/Audit")) },
  { path: "reports", key: "reports", group: "system", perm: "reports.read", icon: ChartColumn, page: lazy(() => import("./pages/Reports")) },
  { path: "settings", key: "settings", group: "system", icon: Settings, page: lazy(() => import("./pages/system/Settings")) },
];

/** Pages under a section that the sidebar does not list (a record's own page). */
export const subpages: Section[] = [
  { path: "users/:id", key: "users", group: "users", perm: "users.read", icon: Users, page: lazy(() => import("./pages/users/UserPage")) },
];

/** allowed reports whether an administrator may open a section. */
export function allowed(admin: Admin, s: Section): boolean {
  if (!s.perm) return true;
  return (Array.isArray(s.perm) ? s.perm : [s.perm]).some((p) => can(admin, p));
}
