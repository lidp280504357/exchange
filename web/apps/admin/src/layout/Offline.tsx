import { WifiOff } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * Offline says the network is gone, at once (design 2026-09-30 §12.2,
 * ui-checklist item 6). Nothing needs a reload when it comes back: the
 * query client holds what was asked for offline and asks again, and
 * refreshes what went stale; the event stream reconnects by itself.
 */
export function Offline() {
  const { t } = useTranslation();
  const [online, setOnline] = useState(() => navigator.onLine);
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
  if (online) return null;
  return (
    <div role="status" className="sticky top-14 z-[var(--z-sticky)] bg-bg-1">
      <div className="flex items-center justify-center gap-2 border-b border-warn/40 bg-warn/10 px-6 py-2 text-sm text-warn-strong">
        <WifiOff size={15} aria-hidden />
        {t("admin.offline")}
      </div>
    </div>
  );
}
