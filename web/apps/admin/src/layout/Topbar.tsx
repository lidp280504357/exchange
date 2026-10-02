import { adminApi, can, type Admin } from "@exchange/core/api/admin";
import { Avatar, Badge, cn, DropdownMenu, IconButton, Popover, Tooltip } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Bell, ChevronRight, LogOut, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useConsoleSettings, useTodo } from "../live";
import { setTheme, useTheme } from "../theme";
import { CountBadge } from "./CountBadge";
import { GlobalSearch } from "./GlobalSearch";

/**
 * Topbar: the environment, the approval mode, the search, what waits for
 * the administrator, the theme and the administrator's menu.
 */
export function Topbar({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const theme = useTheme();
  const settings = useConsoleSettings();
  const qc = useQueryClient();
  const logout = async () => {
    await adminApi.POST("/admin/v1/logout");
    qc.setQueryData(["admin", "me"], null);
  };
  return (
    <header className="sticky top-0 z-[var(--z-sticky)] flex h-14 items-center gap-3 border-b border-line-1 bg-bg-1 px-6">
      <span className="shrink-0 rounded-1 bg-warn px-2 py-0.5 text-xs font-medium text-black">{t("admin.env")}</span>
      {settings.data && (
        <Tooltip content={t(settings.data.two_person_approval ? "admin.shell.twoPersonHint" : "admin.shell.singleHint")}>
          <Link
            to="/settings"
            className={cn(
              "hidden shrink-0 rounded-full border px-2.5 py-0.5 text-xs md:inline-block",
              settings.data.two_person_approval ? "border-success/40 text-success" : "border-info/40 text-info",
            )}
          >
            {t(settings.data.two_person_approval ? "admin.shell.twoPerson" : "admin.shell.single")}
          </Link>
        </Tooltip>
      )}
      <GlobalSearch admin={admin} />
      <div className="ml-auto flex shrink-0 items-center gap-1.5">
        <TodoBell admin={admin} />
        <IconButton
          icon={theme === "dark" ? <Sun /> : <Moon />}
          label={t(theme === "dark" ? "admin.shell.light" : "admin.shell.dark")}
          onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
        />
        <DropdownMenu
          trigger={
            <button type="button" className="ml-1 flex items-center gap-2 rounded-2 px-1.5 py-1 text-sm hover:bg-bg-2" aria-label={t("admin.shell.account")}>
              <Avatar name={admin.name || admin.email} size={28} />
              <span className="hidden max-w-40 truncate text-fg-1 lg:inline">{admin.name || admin.email}</span>
            </button>
          }
          items={[
            { type: "label", key: "who", label: <span className="block max-w-56 truncate">{admin.email}</span> },
            { type: "label", key: "role", label: t(`admin.roles.${admin.role}`) },
            { type: "separator", key: "s1" },
            {
              key: "theme",
              label: t(theme === "dark" ? "admin.shell.light" : "admin.shell.dark"),
              icon: theme === "dark" ? <Sun size={14} /> : <Moon size={14} />,
              onSelect: () => setTheme(theme === "dark" ? "light" : "dark"),
            },
            { type: "separator", key: "s2" },
            { key: "logout", label: t("admin.logout"), icon: <LogOut size={14} />, danger: true, onSelect: () => void logout() },
          ]}
        />
      </div>
    </header>
  );
}

/** TodoBell sums what waits (withdrawals to review, fund operations, identity requests and deposits to decide) and leads to each. */
function TodoBell({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const todo = useTodo();
  const rows = [
    { key: "withdrawals", to: "/withdrawals", n: todo?.withdrawals ?? 0, show: can(admin, "withdrawals.read") },
    { key: "approvals", to: "/approvals", n: todo?.approvals ?? 0, show: can(admin, "ledger.adjust.request") || can(admin, "ledger.adjust.approve") },
    { key: "identityRequests", to: "/identity-requests", n: todo?.identity_requests ?? 0, show: can(admin, "users.security") },
    { key: "deposits", to: "/deposits?view=attention", n: todo?.deposits ?? 0, show: can(admin, "deposits.review") },
  ].filter((r) => r.show);
  const total = rows.reduce((n, r) => n + r.n, 0);
  return (
    <Popover
      align="end"
      aria-label={t("admin.shell.todo")}
      trigger={
        <button type="button" className="relative grid size-8 place-items-center rounded-2 text-fg-2 hover:bg-bg-2 hover:text-fg-1" aria-label={t("admin.shell.todoCount", { n: total })}>
          <Bell size={17} />
          {total > 0 && <CountBadge value={total} className="absolute -right-1 -top-1 h-4 min-w-4 px-1 text-[10px]" />}
        </button>
      }
      className="w-64 p-2"
    >
      <div className="px-2 pb-1.5 pt-1 text-xs font-medium text-fg-3">{t("admin.shell.todo")}</div>
      {rows.map((r) => (
        <Link key={r.key} to={r.to} className="flex items-center gap-2 rounded-1 px-2 py-2 text-sm hover:bg-bg-3">
          <span className="flex-1">{t(`admin.shell.todo_${r.key}`)}</span>
          <Badge tone={r.n ? "warn" : "neutral"}>{r.n}</Badge>
          <ChevronRight size={14} className="text-fg-3" />
        </Link>
      ))}
      {todo?.partial.length ? <p className="px-2 pt-1 text-xs text-fg-3">{t("admin.partial", { parts: todo.partial.join(", ") })}</p> : null}
    </Popover>
  );
}
