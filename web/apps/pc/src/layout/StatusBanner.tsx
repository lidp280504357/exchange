import { useWsStatus } from "@exchange/core";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * StatusBanner says when the network or the live connection is gone
 * (design §12.2): offline at once, a lost WebSocket after 3 seconds of
 * reconnecting (a short blip is not worth a banner). It sticks under the
 * top bar one layer below it: over the pages' sticky headers, under the
 * top bar's menus (a reconnect while 合约 was open covered its first
 * item, webflows 2026-10-06).
 */
export function StatusBanner() {
  const { t } = useTranslation();
  const status = useWsStatus();
  const [online, setOnline] = useState(() => navigator.onLine);
  const [late, setLate] = useState(false);
  useEffect(() => {
    const on = () => setOnline(true);
    const off = () => setOnline(false);
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
    };
  }, []);
  useEffect(() => {
    if (status !== "reconnecting") {
      setLate(false);
      return;
    }
    const id = setTimeout(() => setLate(true), 3000);
    return () => clearTimeout(id);
  }, [status]);
  if (online && !late) return null;
  return (
    <div role="status" className="sticky top-14 z-[calc(var(--z-topbar)-1)] animate-slide-down bg-warn px-4 py-1.5 text-center text-sm text-brand-fg">
      {online ? t("common.reconnecting") : t("common.offline")}
    </div>
  );
}
