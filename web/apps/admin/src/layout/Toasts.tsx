import { cn, toastStore, useToasts, type ToastRecord } from "@exchange/ui";
import { CircleX, Info, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Check } from "../kit/Check";

/**
 * Toasts are the console's messages (design 2026-10-02 §6): they slide in
 * at the bottom right, a success with its check mark drawn; hovering one
 * keeps it. They render the shared toast store, so toast.success() and
 * friends work as on the other sites. At the top they covered a drawer's
 * buttons right after its action, and a click on one counted as a click
 * outside the drawer and closed it: a press on a toast stops here.
 */
export function Toasts() {
  const { t } = useTranslation();
  const items = useToasts();
  return (
    <section
      aria-label={t("ui.notifications")}
      onPointerDown={(e) => e.stopPropagation()}
      className="pointer-events-none fixed bottom-0 right-0 z-[var(--z-toast)] flex w-full max-w-sm flex-col gap-2 p-4"
    >
      {items.map((item) => (
        <Toast key={item.id} item={item} />
      ))}
    </section>
  );
}

function Toast({ item }: { item: ToastRecord }) {
  const { t } = useTranslation();
  return (
    <div
      role={item.tone === "error" ? "alert" : "status"}
      aria-live={item.tone === "error" ? "assertive" : "polite"}
      onMouseEnter={() => toastStore.pause(item.id)}
      onMouseLeave={() => toastStore.resume(item.id)}
      onFocus={() => toastStore.pause(item.id)}
      onBlur={() => toastStore.resume(item.id)}
      className={cn(
        "card pointer-events-auto flex w-full gap-3 border-l-[3px] p-3 text-fg-1 animate-toast-in",
        item.tone === "success" ? "border-l-success" : item.tone === "error" ? "border-l-danger" : "border-l-info",
      )}
    >
      <span className="mt-0.5 shrink-0">
        {item.tone === "success" ? <Check /> : item.tone === "error" ? <CircleX size={18} className="text-danger" /> : <Info size={18} className="text-info" />}
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{item.title}</div>
        {item.description && <div className="mt-0.5 break-words text-xs text-fg-3">{item.description}</div>}
        {item.action && (
          <button
            type="button"
            onClick={() => {
              item.action?.onClick();
              toastStore.dismiss(item.id);
            }}
            className="mt-2 text-xs font-medium text-brand-strong hover:brightness-110"
          >
            {item.action.label}
          </button>
        )}
      </div>
      <button
        type="button"
        aria-label={t("ui.dismiss")}
        onClick={() => toastStore.dismiss(item.id)}
        className="-mr-1 -mt-1 grid size-7 shrink-0 place-items-center rounded-1 text-fg-3 hover:bg-bg-2 hover:text-fg-1"
      >
        <X size={14} />
      </button>
    </div>
  );
}
