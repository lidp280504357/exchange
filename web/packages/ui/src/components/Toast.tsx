import { CircleCheck, CircleX, Info, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion, type PanInfo } from "motion/react";
import { useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { durations, ease } from "../lib/motion";

// Toasts (design §6.2: "下单成功" with "查看委托", errors with their
// text): toast() adds one to a tiny store outside React, the one Toaster
// of the app renders the store. Each toast leaves after 4 s; hovering
// pauses the clock, so an action can still be reached.

export type ToastTone = "success" | "error" | "info";

export type ToastAction = { label: ReactNode; onClick: () => void };

export type ToastOptions = {
  title: ReactNode;
  description?: ReactNode;
  tone?: ToastTone;
  action?: ToastAction;
  /** ms before it leaves (default 4000); 0 keeps it until dismissed. */
  duration?: number;
  /** Reuse an id to replace a toast in place ("提交中" → "已提交"). */
  id?: string;
};

export type ToastRecord = {
  id: string;
  title: ReactNode;
  description?: ReactNode;
  tone: ToastTone;
  action?: ToastAction;
  duration: number;
};

export const TOAST_DURATION = 4000;
/** At most this many toasts show; older ones leave first. */
export const TOAST_LIMIT = 4;

type Timer = { handle?: ReturnType<typeof setTimeout>; remaining: number; startedAt: number };

/** ToastStore keeps the toasts and their timers; exported for tests. */
export class ToastStore {
  private items: ToastRecord[] = [];
  private listeners = new Set<() => void>();
  private timers = new Map<string, Timer>();
  private seq = 0;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  getSnapshot = (): ToastRecord[] => this.items;

  /** add shows a toast (or replaces the one with the same id); returns its id. */
  add(opts: ToastOptions): string {
    const id = opts.id ?? `t${++this.seq}`;
    const record: ToastRecord = {
      id,
      title: opts.title,
      description: opts.description,
      tone: opts.tone ?? "info",
      action: opts.action,
      duration: opts.duration ?? TOAST_DURATION,
    };
    const at = this.items.findIndex((t) => t.id === id);
    if (at >= 0) {
      this.items = this.items.map((t) => (t.id === id ? record : t));
    } else {
      this.items = [...this.items, record];
      while (this.items.length > TOAST_LIMIT) {
        const oldest = this.items[0];
        if (oldest) this.clearTimer(oldest.id);
        this.items = this.items.slice(1);
      }
    }
    this.clearTimer(id);
    if (record.duration > 0) this.startTimer(id, record.duration);
    this.emit();
    return id;
  }

  /** dismiss removes one toast, or all of them without an id. */
  dismiss(id?: string): void {
    if (id === undefined) {
      for (const t of this.items) this.clearTimer(t.id);
      this.items = [];
    } else {
      this.clearTimer(id);
      this.items = this.items.filter((t) => t.id !== id);
    }
    this.emit();
  }

  /** pause stops a toast's clock (hover, focus). */
  pause(id: string): void {
    const timer = this.timers.get(id);
    if (!timer?.handle) return;
    clearTimeout(timer.handle);
    timer.handle = undefined;
    timer.remaining = Math.max(0, timer.remaining - (Date.now() - timer.startedAt));
  }

  /** resume restarts a paused clock with the time that was left. */
  resume(id: string): void {
    const timer = this.timers.get(id);
    if (!timer || timer.handle) return;
    this.startTimer(id, timer.remaining);
  }

  private startTimer(id: string, ms: number) {
    const timer: Timer = { remaining: ms, startedAt: Date.now() };
    timer.handle = setTimeout(() => this.dismiss(id), ms);
    this.timers.set(id, timer);
  }

  private clearTimer(id: string) {
    const timer = this.timers.get(id);
    if (timer?.handle) clearTimeout(timer.handle);
    this.timers.delete(id);
  }

  private emit() {
    for (const fn of this.listeners) fn();
  }
}

/** The app's toast store (one per page). */
export const toastStore = new ToastStore();

type ToastFn = ((opts: ToastOptions) => string) & {
  success: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => string;
  error: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => string;
  info: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => string;
  dismiss: (id?: string) => void;
};

/** toast shows a message: toast({ title, description, tone, action }). */
export const toast: ToastFn = Object.assign((opts: ToastOptions) => toastStore.add(opts), {
  success: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => toastStore.add({ ...opts, title, tone: "success" }),
  error: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => toastStore.add({ ...opts, title, tone: "error" }),
  info: (title: ReactNode, opts?: Omit<ToastOptions, "title" | "tone">) => toastStore.add({ ...opts, title, tone: "info" }),
  dismiss: (id?: string) => toastStore.dismiss(id),
});

/** useToasts follows a store's toasts. */
export function useToasts(store: ToastStore = toastStore): ToastRecord[] {
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}

export type ToasterProps = {
  /** bottom-right on the PC site and the console, top on the mobile site. */
  position?: "bottom-right" | "top";
  store?: ToastStore;
  className?: string;
};

const icons: Record<ToastTone, ReactNode> = {
  success: <CircleCheck size={18} className="text-success" />,
  error: <CircleX size={18} className="text-danger" />,
  info: <Info size={18} className="text-info" />,
};

/** Toaster renders the toasts; put one near the root of each app. */
export function Toaster({ position = "bottom-right", store = toastStore, className }: ToasterProps) {
  const { t } = useTranslation();
  const items = useToasts(store);
  const reduced = useReducedMotion();
  const top = position === "top";
  const offset = top ? -16 : 16;
  return (
    <section
      aria-label={t("ui.notifications")}
      className={cn(
        "pointer-events-none fixed z-[var(--z-toast)] flex w-full flex-col gap-2 p-4",
        top ? "inset-x-0 top-0 items-center" : "bottom-0 right-0 max-w-sm items-end",
        className,
      )}
      style={top ? { paddingTop: "max(16px, env(safe-area-inset-top))" } : undefined}
    >
      <AnimatePresence initial={false}>
        {(top ? [...items].reverse() : items).map((item) => (
          <motion.div
            key={item.id}
            layout={!reduced}
            role={item.tone === "error" ? "alert" : "status"}
            aria-live={item.tone === "error" ? "assertive" : "polite"}
            initial={reduced ? false : { opacity: 0, y: offset, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1, transition: { duration: reduced ? 0 : durations.base, ease } }}
            exit={{ opacity: 0, scale: 0.96, transition: { duration: reduced ? 0 : durations.fast, ease } }}
            drag={top ? "y" : "x"}
            dragConstraints={{ left: 0, right: 0, top: 0, bottom: 0 }}
            dragElastic={top ? { top: 0.6, bottom: 0 } : { left: 0, right: 0.6 }}
            onDragEnd={(_, info: PanInfo) => {
              if ((top && info.offset.y < -40) || (!top && info.offset.x > 80)) store.dismiss(item.id);
            }}
            onMouseEnter={() => store.pause(item.id)}
            onMouseLeave={() => store.resume(item.id)}
            onFocus={() => store.pause(item.id)}
            onBlur={() => store.resume(item.id)}
            className="pointer-events-auto flex w-full max-w-sm gap-3 rounded-2 border border-line-2 bg-bg-2 p-3 text-fg-1 shadow-pop"
          >
            <span className="mt-0.5 shrink-0">{icons[item.tone]}</span>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{item.title}</div>
              {item.description && <div className="mt-0.5 break-words text-xs text-fg-3">{item.description}</div>}
              {item.action && (
                <button
                  type="button"
                  onClick={() => {
                    item.action?.onClick();
                    store.dismiss(item.id);
                  }}
                  className="mt-2 text-xs font-medium text-brand hover:brightness-110"
                >
                  {item.action.label}
                </button>
              )}
            </div>
            <button
              type="button"
              aria-label={t("ui.dismiss")}
              onClick={() => store.dismiss(item.id)}
              className="-mr-1 -mt-1 grid size-7 shrink-0 place-items-center rounded-1 text-fg-3 hover:bg-bg-3 hover:text-fg-1"
            >
              <X size={14} />
            </button>
          </motion.div>
        ))}
      </AnimatePresence>
    </section>
  );
}
