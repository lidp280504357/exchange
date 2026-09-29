import { useMutation } from "@tanstack/react-query";
import { NavLink, Navigate, Route, Routes } from "react-router";
import { api, describe, type Admin, type Permission } from "./api/client";
import { AuditPage } from "./pages/Audit";
import { FlagsPage } from "./pages/Flags";
import { InstrumentsPage } from "./pages/Instruments";
import { LedgerPage } from "./pages/Ledger";
import { LoginPage } from "./pages/Login";
import { UsersPage } from "./pages/Users";
import { WithdrawalsPage } from "./pages/Withdrawals";
import { can, roleNames, useMe } from "./session";
import { Button, ErrorText } from "./ui";

const sections: { path: string; label: string; perm: Permission; page: (props: { admin: Admin }) => React.ReactNode }[] = [
  { path: "withdrawals", label: "提现审批", perm: "withdrawals.read", page: WithdrawalsPage },
  { path: "users", label: "用户", perm: "users.read", page: UsersPage },
  { path: "instruments", label: "资产与交易对", perm: "instruments.read", page: InstrumentsPage },
  { path: "flags", label: "功能开关", perm: "flags.read", page: FlagsPage },
  { path: "ledger", label: "调账审批", perm: "audit.read", page: LedgerPage },
  { path: "audit", label: "审计日志", perm: "audit.read", page: AuditPage },
];

export function App() {
  const me = useMe();
  if (me.isPending) return <p className="p-8 text-sm text-slate-500">加载中…</p>;
  if (me.isError) return <ErrorText text={describe(me.error)} />;
  if (!me.data) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }
  const admin = me.data;
  const visible = sections.filter((s) => can(admin, s.perm));
  return (
    <div className="min-h-screen">
      <Header admin={admin} />
      <nav className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-7xl flex-wrap gap-1 px-4">
          {visible.map((s) => (
            <NavLink
              key={s.path}
              to={`/${s.path}`}
              className={({ isActive }) =>
                `border-b-2 px-3 py-2 text-sm ${isActive ? "border-slate-800 font-medium text-slate-900" : "border-transparent text-slate-500 hover:text-slate-800"}`
              }
            >
              {s.label}
            </NavLink>
          ))}
        </div>
      </nav>
      <main className="mx-auto max-w-7xl space-y-4 p-4">
        <Routes>
          {visible.map((s) => (
            <Route key={s.path} path={`/${s.path}`} element={<s.page admin={admin} />} />
          ))}
          <Route path="*" element={<Navigate to={`/${visible[0]?.path ?? "audit"}`} replace />} />
        </Routes>
      </main>
    </div>
  );
}

function Header({ admin }: { admin: Admin }) {
  // A full reload drops everything the console loaded.
  const logout = useMutation({
    mutationFn: () => api.POST("/admin/v1/logout"),
    onSettled: () => window.location.replace("/admin/login"),
  });
  return (
    <header className="bg-slate-800 text-white">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 py-2.5">
        <span className="font-semibold">Exchange 管理后台</span>
        <div className="flex items-center gap-3 text-sm">
          <span data-testid="whoami">
            {admin.name}（{admin.email}，{roleNames[admin.role]}）
          </span>
          <Button variant="inverse" disabled={logout.isPending} onClick={() => logout.mutate()}>
            退出
          </Button>
        </div>
      </div>
    </header>
  );
}
