import { adminApi, adminData, can } from "@exchange/core/api/admin";
import { Badge, Button, ConfirmDialog, Dialog, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../../kit/actions";
import { useMe } from "../../session";
import { changesKey, GuardNote, scheduledText, type ChangeGuard, type InstrumentChange } from "./changes";
import { fieldDiffs, type ConfigChange, type ConfigPatch, type ConfigResult } from "./config";

type Pending = { patch: ConfigPatch; result: ConfigResult; guard: ChangeGuard; title: ReactNode; onApplied?: () => void };

/**
 * useReview previews a patch of the reference data and, when it changes
 * anything, asks to apply it: the changes field by field, the console's
 * notes and, for trading parameters, what they are, what a new risk
 * ladder would liquidate and how they take effect (design 2026-10-02 §2
 * item 6); a reason and a confirmation word (the first item's key).
 * Nothing is applied before the confirmation; trading parameters are
 * confirmed with the preview's token and then wait.
 */
export function useReview() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const admin = useMe().data;
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const review = async (patch: ConfigPatch, title: ReactNode, onApplied?: () => void) => {
    setBusy(true);
    try {
      const { guard, ...result } = adminData(await adminApi.POST("/admin/v1/instruments/preview", { body: { config: patch } }));
      if (result.changes.length === 0) {
        toast.info(t("admin.listing.nothing"));
        return;
      }
      setPending({ patch, result, guard, title, onApplied });
    } catch (err) {
      errorToast(err);
    } finally {
      setBusy(false);
    }
  };
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "instruments"] });
    void qc.invalidateQueries({ queryKey: ["admin", "derivatives"] });
    void qc.invalidateQueries({ queryKey: changesKey });
    void qc.invalidateQueries({ queryKey: ["admin", "todo"] });
  };
  const guarded = (pending?.guard.params.length ?? 0) > 0;
  const confirmable = !guarded || (!!pending?.guard.confirmation && can(admin, "instruments.trading"));
  const body = pending && (
    <div className="flex flex-col gap-3">
      <GuardNote guard={pending.guard} canConfirm={can(admin, "instruments.trading")} />
      <Changes result={pending.result} />
    </div>
  );
  const dialog =
    pending &&
    (confirmable ? (
      <ConfirmDialog
        open
        onOpenChange={(o) => !o && setPending(null)}
        title={pending.title}
        description={t("admin.listing.reviewHint")}
        target={t("admin.listing.summary", { n: pending.result.changes.length, unchanged: pending.result.unchanged })}
        confirmWord={pending.result.changes[0]?.key ?? ""}
        confirmText={t("admin.listing.apply")}
        danger={guarded}
        onConfirm={async (reason) => {
          try {
            const res = adminData(
              await adminApi.POST("/admin/v1/instruments/apply", {
                body: { config: pending.patch, reason, confirmation: pending.guard.confirmation?.token },
              }),
            );
            const change = ("change" in res ? res.change : null) as InstrumentChange | null;
            if (change) toast.success(scheduledText(t, change, pending.guard.delay_seconds));
            else toast.success(t("admin.listing.applied", { n: res.changes.length }));
            refresh();
            pending.onApplied?.();
            setPending(null);
          } catch (err) {
            errorToast(err);
          }
        }}
      >
        {body}
      </ConfirmDialog>
    ) : (
      <Dialog
        open
        onOpenChange={(o) => !o && setPending(null)}
        title={pending.title}
        size="lg"
        footer={<Button onClick={() => setPending(null)}>{t("admin.common.close")}</Button>}
      >
        {body}
      </Dialog>
    ));
  return { review, busy, dialog };
}

/** Changes lists what a patch changes, item by item, and the console's notes. */
export function Changes({ result }: { result: ConfigResult }) {
  const { t } = useTranslation();
  return (
    <div className="flex max-h-[45vh] flex-col gap-3 overflow-y-auto">
      {result.warnings.length > 0 && (
        <ul className="flex flex-col gap-1 rounded-2 border border-warn/40 bg-warn/10 p-3 text-sm">
          {result.warnings.map((w) => (
            <li key={`${w.code}/${w.symbol}`} className="flex gap-2">
              <TriangleAlert size={14} className="mt-0.5 shrink-0 text-warn-strong" />
              <span className="text-fg-2">{t(`admin.listing.warnings.${w.code}`, { symbol: w.symbol, detail: w.detail })}</span>
            </li>
          ))}
        </ul>
      )}
      {result.changes.map((c) => (
        <Change key={`${c.entity}/${c.key}`} change={c} />
      ))}
      {result.unchanged > 0 && <p className="text-xs text-fg-3">{t("admin.listing.unchanged", { n: result.unchanged })}</p>}
    </div>
  );
}

function Change({ change }: { change: ConfigChange }) {
  const { t } = useTranslation();
  const diffs = fieldDiffs(change.before as Record<string, unknown> | null, change.after as Record<string, unknown>);
  return (
    <section className="rounded-2 border border-line-1">
      <header className="flex flex-wrap items-center gap-2 border-b border-line-1 px-3 py-2 text-sm">
        <Badge tone={change.action === "CREATE" ? "success" : "info"}>{t(`admin.listing.actions.${change.action}`)}</Badge>
        <span className="text-fg-3">{t(`admin.listing.entities.${change.entity}`)}</span>
        <span className="font-mono font-medium">{change.key}</span>
        <span className="ml-auto text-xs text-fg-3">v{change.version}</span>
      </header>
      <table className="w-full text-left text-xs">
        <tbody>
          {diffs.map((d) => (
            <tr key={d.field} className="border-t border-line-1 first:border-t-0">
              <td className="w-40 px-3 py-1.5 text-fg-3">{t(`admin.listing.fields.${d.field}`, { defaultValue: d.field })}</td>
              {change.action === "UPDATE" && <td className="px-3 py-1.5 font-mono text-fg-3 line-through">{d.before}</td>}
              <td className="px-3 py-1.5 font-mono text-fg-1">{d.after}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}


