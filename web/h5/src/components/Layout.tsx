import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet, useNavigate } from "react-router";
import { authApi } from "../api/client";
import { setLanguage } from "../i18n";
import { PrivateSocket } from "../lib/ws";
import { useSession } from "../store/session";
import { refreshTokens } from "../lib/native";

// useLiveEvents keeps the WebSocket open while signed in and refreshes the
// affected queries on each push (§7.3).
function useLiveEvents() {
  const qc = useQueryClient();
  const token = useSession((s) => s.session?.accessToken);
  const signedIn = Boolean(token);
  const [socket] = useState(() => new PrivateSocket(["balances", "notifications", "orders", "fills", "deposits", "withdrawals"]));
  const [flash, setFlash] = useState("");

  useEffect(() => {
    if (!signedIn) return;
    const off = socket.on((p) => {
      if (p.channel === "balances") {
        void qc.invalidateQueries({ queryKey: ["balances"] });
        void qc.invalidateQueries({ queryKey: ["ledger"] });
      } else if (p.channel === "orders") {
        void qc.invalidateQueries({ queryKey: ["orders"] });
      } else if (p.channel === "fills") {
        void qc.invalidateQueries({ queryKey: ["fills"] });
        void qc.invalidateQueries({ queryKey: ["orders"] });
      } else if (p.channel === "deposits") {
        void qc.invalidateQueries({ queryKey: ["deposits"] });
      } else if (p.channel === "withdrawals") {
        void qc.invalidateQueries({ queryKey: ["withdrawals"] });
      } else if (p.channel === "notifications") {
        void qc.invalidateQueries({ queryKey: ["notifications"] });
        setFlash(p.data.title ?? "");
      } else if (p.channel === "resync") {
        void qc.invalidateQueries();
      }
    });
    socket.start();
    return () => {
      off();
      socket.stop();
    };
  }, [signedIn, socket, qc]);

  // A refreshed token keeps the socket authenticated.
  useEffect(() => {
    if (token) socket.reauth(token);
  }, [token, socket]);

  useEffect(() => {
    if (!flash) return;
    const t = setTimeout(() => setFlash(""), 5000);
    return () => clearTimeout(t);
  }, [flash]);
  return flash;
}

const nav = [
  ["/", "nav.assets"],
  ["/markets", "nav.markets"],
  ["/transfer", "nav.transfer"],
  ["/notifications", "nav.notifications"],
  ["/security", "nav.security"],
  ["/settings", "nav.profile"],
] as const;

export function Layout() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const setSession = useSession((s) => s.set);
  const flash = useLiveEvents();

  async function logout() {
    await authApi.POST("/v1/auth/logout").catch(() => undefined);
    await refreshTokens.clear(); // the desktop app's stored token
    setSession(null);
    navigate("/login");
  }

  const link = ({ isActive }: { isActive: boolean }) => `rounded-md px-3 py-1.5 text-sm ${isActive ? "bg-white/10 text-white" : "text-gray-400 hover:text-white"}`;
  return (
    <div className="mx-auto min-h-screen max-w-5xl pb-20 sm:pb-6">
      <header className="flex items-center justify-between gap-2 border-b border-white/10 px-4 py-3">
        <span className="font-bold text-[#f0b90b]">{t("app.name")}</span>
        <nav className="hidden gap-1 sm:flex">
          {nav.map(([to, key]) => <NavLink key={to} to={to} end className={link}>{t(key)}</NavLink>)}
        </nav>
        <div className="flex items-center gap-3 text-sm">
          {/* The bottom bar of phones has room for five items; settings live here. */}
          <NavLink to="/settings" className="text-gray-400 sm:hidden">{t("nav.profile")}</NavLink>
          <button className="text-gray-400" onClick={() => setLanguage(i18n.language === "en" ? "zh-CN" : "en")}>{i18n.language === "en" ? "中文" : "EN"}</button>
          <button className="text-gray-400" onClick={logout}>{t("nav.logout")}</button>
        </div>
      </header>
      {flash && <div className="mx-4 mt-3 rounded-md bg-[#f0b90b]/15 px-3 py-2 text-sm text-[#f8d12f]">{flash}</div>}
      <main className="space-y-4 p-4">
        <p className="text-xs text-gray-500">{t("app.tagline")}</p>
        <Outlet />
      </main>
      <nav className="fixed inset-x-0 bottom-0 flex justify-around border-t border-white/10 bg-[#0b0e11] py-2 sm:hidden">
        {nav.slice(0, 5).map(([to, key]) => <NavLink key={to} to={to} end className={link}>{t(key)}</NavLink>)}
      </nav>
    </div>
  );
}
