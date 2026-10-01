import type { Admin, Permission } from "@exchange/core/api/admin";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  BookOpen,
  ChartColumn,
  ChartLine,
  Coins,
  Landmark,
  LayoutDashboard,
  ListOrdered,
  ScrollText,
  ShieldAlert,
  Users,
  Warehouse,
  type LucideIcon,
} from "lucide-react";
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

export type Section = {
  path: string;
  key: string;
  perm: Permission;
  icon: LucideIcon;
  page: LazyExoticComponent<ComponentType<{ admin: Admin }>>;
};

/** The console's sections (design §10.1); a section hides without its permission. */
export const sections: Section[] = [
  { path: "", key: "overview", perm: "reports.read", icon: LayoutDashboard, page: lazy(() => import("./pages/Overview")) },
  { path: "users", key: "users", perm: "users.read", icon: Users, page: lazy(() => import("./pages/users/Users")) },
  { path: "orders", key: "orders", perm: "reports.read", icon: ListOrdered, page: lazy(() => import("./pages/orders/Orders")) },
  { path: "deposits", key: "deposits", perm: "withdrawals.read", icon: ArrowDownToLine, page: lazy(() => import("./pages/wallet/Deposits")) },
  { path: "withdrawals", key: "withdrawals", perm: "withdrawals.read", icon: ArrowUpFromLine, page: lazy(() => import("./pages/wallet/Withdrawals")) },
  { path: "custody", key: "custody", perm: "withdrawals.read", icon: Landmark, page: lazy(() => import("./pages/wallet/Custody")) },
  { path: "instruments", key: "instruments", perm: "instruments.read", icon: Coins, page: lazy(() => import("./pages/Instruments")) },
  { path: "derivatives", key: "derivatives", perm: "derivatives.read", icon: ChartLine, page: lazy(() => import("./pages/Derivatives")) },
  { path: "house", key: "house", perm: "reports.read", icon: Warehouse, page: lazy(() => import("./pages/House")) },
  { path: "risk", key: "risk", perm: "flags.read", icon: ShieldAlert, page: lazy(() => import("./pages/Flags")) },
  { path: "ledger", key: "ledger", perm: "reports.read", icon: BookOpen, page: lazy(() => import("./pages/Ledger")) },
  { path: "audit", key: "audit", perm: "audit.read", icon: ScrollText, page: lazy(() => import("./pages/Audit")) },
  { path: "reports", key: "reports", perm: "reports.read", icon: ChartColumn, page: lazy(() => import("./pages/Reports")) },
];
