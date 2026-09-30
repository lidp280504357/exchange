import { useWsStatus } from "@exchange/core";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * StatusStrip says when the network or the live connection is gone
 * (design §12.2): offline at once, a lost WebSocket after 3 seconds.
 */
export function StatusStrip() {
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
    <div role="status" className="animate-slide-down bg-warn px-4 py-1 text-center text-xs text-brand-fg">
      {online ? t("common.reconnecting") : t("common.offline")}
    </div>
  );
}
