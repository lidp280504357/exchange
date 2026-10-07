import { ApiError, formatDecimal } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { Badge, Button, Checkbox, Combobox, Input, Progress, Segmented, Skeleton, type ComboboxItem } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { Card } from "../../kit/Page";
import { useInstrumentConfig } from "../instruments/config";
import { EndEvent, pct, simEventsKey, simKey, useEventText, utc, type SimEvent } from "./common";
import { moveOf, newOverlayDraft, OVERLAY_MAX_PAIRS, overlayBody, targetOf, type OverlayDraft } from "./overlayRules";

// Price events on any pair (design 2026-10-07, general price control, J3;
// market-sim's J2): a pair following Binance has its reference data ramp
// to a target and back to Binance's price within seconds, the platform
// coin's pair jumps by its model. One operator's share is counted by pair;
// beyond it the request waits for a second administrator.

/** usePrices reads every listed pair's last price (market-data's tickers) every few seconds. */
function usePrices() {
  return useQuery({
    queryKey: [...simKey, "prices"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/prices")).prices,
    refetchInterval: 5_000,
  });
}

/** shown is a pair's price as the form shows it: grouped, as precise as it came. */
const shown = (v: string | null | undefined) => (v ? formatDecimal(v) : "—");

/** secondsOf is one of the three times as a change of the draft. */
const secondsOf = (k: "up" | "hold" | "down", v: string): Partial<OverlayDraft> => (k === "up" ? { up: v } : k === "hold" ? { hold: v } : { down: v });

/**
 * OverlayCard is the form of a price event on any pair with the events
 * running and queued; simSymbol is the platform coin's pair (market-sim's),
 * which jumps by its model.
 */
export function OverlayCard({ control, simSymbol }: { control: boolean; simSymbol?: string }) {
  const { t } = useTranslation();
  return (
    <Card title={t("admin.sim.overlay.title")} extra={<span className="text-xs text-fg-3">{t("admin.sim.share")}</span>}>
      <div className="flex flex-col gap-4" data-testid="overlay-card">
        <p className="text-sm text-fg-3">{t("admin.sim.overlay.help")}</p>
        <OpenOverlays control={control} />
        {control && <OverlayForm simSymbol={simSymbol} />}
      </div>
    </Card>
  );
}

/** OpenOverlays lists the price events running and queued: their factor, progress and restoring them. */
function OpenOverlays({ control }: { control: boolean }) {
  const { t } = useTranslation();
  const eventText = useEventText();
  const q = useQuery({
    queryKey: [...simEventsKey, "overlays"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/events")).items,
    refetchInterval: 2_000,
  });
  if (q.isPending) return <Skeleton className="h-10 w-full" />;
  const open = (q.data ?? []).filter((e) => e.type === "OVERLAY" && (e.status === "RUNNING" || e.status === "SCHEDULED"));
  return (
    <div className="flex flex-col gap-1.5" data-testid="overlay-open">
      <span className="text-xs font-medium text-fg-2">{t("admin.sim.overlay.open")}</span>
      {open.length === 0 ? (
        <span className="text-xs text-fg-3">{t("admin.sim.overlay.noneOpen")}</span>
      ) : (
        open.map((e) => <OpenOverlay key={e.id} e={e} text={eventText(e)} control={control} />)
      )}
    </div>
  );
}

function OpenOverlay({ e, text, control }: { e: SimEvent; text: string; control: boolean }) {
  const { t } = useTranslation();
  const running = e.status === "RUNNING";
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-2 border border-line-1 px-3 py-2 text-sm" data-testid={`overlay-${e.id}`}>
      <Badge tone={running ? "brand" : "info"} dot={running}>
        {t(`admin.sim.eventStatus.${e.status}`)}
      </Badge>
      <span className="font-medium text-fg-1">{text}</span>
      {running ? (
        <span className="flex items-center gap-2 text-xs text-fg-2">
          <Progress value={(e.progress ?? 0) * 100} className="w-24" aria-label={t("admin.sim.overlay.factor", { f: e.factor_now ?? 1 })} />
          <span className="font-mono">{t("admin.sim.overlay.factor", { f: (e.factor_now ?? 1).toFixed(4) })}</span>
        </span>
      ) : (
        <span className="text-xs text-fg-2">
          {t("admin.sim.begins")} <TimeText value={e.starts_at} style="datetimeSeconds" />
        </span>
      )}
      {control && (
        <span className="ml-auto">
          <EndEvent e={e} text={text} />
        </span>
      )}
    </div>
  );
}

/** OverlayForm asks for a price event: the pairs, the target, three times in seconds, the leverage reached or spared. */
function OverlayForm({ simSymbol }: { simSymbol?: string }) {
  const { t } = useTranslation();
  const cfg = useInstrumentConfig();
  const prices = usePrices();
  const [d, setD] = useState<OverlayDraft>(newOverlayDraft);
  const [startsAt, setStartsAt] = useState("");
  const set = (patch: Partial<OverlayDraft>) => setD({ ...d, ...patch });
  // The pairs a price event moves: those following Binance that trade, and the platform coin's.
  const items = useMemo<ComboboxItem[]>(() => {
    const followed = (cfg.data?.pairs ?? []).filter((p) => p.reference_symbol && p.status === "TRADING" && p.symbol !== simSymbol).map((p) => p.symbol);
    const all = [...(simSymbol ? [simSymbol] : []), ...followed.sort()];
    return all.map((s) => ({
      value: s,
      label: s,
      description: s === simSymbol ? t("admin.sim.overlay.platformCoin") : undefined,
      trailing: <span className="font-mono text-xs text-fg-3">{shown(prices.data?.[s])}</span>,
      disabled: d.symbols.includes(s),
    }));
  }, [cfg.data, simSymbol, prices.data, d.symbols, t]);
  const checked = overlayBody(d);
  const problem = "problem" in checked ? t(`admin.sim.overlay.bad.${checked.problem}`, checked.vars) : "";
  const total = [d.up, d.hold, d.down].reduce((s, v) => s + (Number(v) || 0), 0);
  const platform = simSymbol && d.symbols.includes(simSymbol) ? simSymbol : null;
  // Each pair from its price now to where the target takes it.
  const lines = d.symbols.map((s) => {
    const last = prices.data?.[s];
    const to = targetOf(d, last);
    return { symbol: s, last, to, move: moveOf(to, last) };
  });
  const big = lines.some((l) => l.move !== null && Math.abs(l.move) > 0.1);

  const create = async (reason: string) => {
    if ("problem" in checked) throw new FormError(problem);
    if (startsAt && !utc(startsAt)) throw new FormError(t("admin.sim.badTime"));
    const body = { ...checked.body, reason, ...(startsAt ? { starts_at: utc(startsAt) } : {}) };
    try {
      return adminData(await adminApi.POST("/admin/v1/sim/events", { body }));
    } catch (err) {
      // market-sim's refusals, said with the pair and the numbers they name.
      if (err instanceof ApiError) {
        const symbol = String(err.details.symbol ?? "");
        switch (err.code) {
          case "SIM_OVERLAY_LOSS_CAP":
            throw new FormError(
              err.details.estimate_usdt
                ? t("admin.sim.overlay.lossCap", { symbol, estimate: formatDecimal(String(err.details.estimate_usdt)), cap: formatDecimal(String(err.details.cap_usdt)) })
                : t("admin.sim.overlay.lossUnknown", { symbol, reason: String(err.details.reason ?? "") }),
            );
          case "SIM_OVERLAY_RUNNING":
            if (symbol) throw new FormError(t("admin.sim.overlay.running", { symbol }));
            break;
          case "SIM_OVERLAY_TOO_FAR":
            throw new FormError(t("admin.sim.overlay.tooFar", { symbol }));
          case "SIM_NOT_OVERLAYABLE":
            throw new FormError(t("admin.sim.overlay.notOverlayable", { symbol }));
        }
      }
      throw err;
    }
  };

  return (
    <div className="flex flex-col gap-3 border-t border-line-1 pt-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm text-fg-2">{t("admin.sim.overlay.pairs")}</span>
        {d.symbols.map((s) => (
          <span key={s} className="inline-flex items-center gap-1 rounded-1 border border-line-1 bg-bg-2 px-2 py-0.5 font-mono text-xs" data-testid={`overlay-pair-${s}`}>
            {s}
            <button type="button" onClick={() => set({ symbols: d.symbols.filter((x) => x !== s) })} aria-label={t("admin.sim.overlay.remove", { symbol: s })}>
              <X size={12} />
            </button>
          </span>
        ))}
        {d.symbols.length < OVERLAY_MAX_PAIRS && (
          <Combobox
            items={items}
            value={null}
            onValueChange={(v) => set({ symbols: [...d.symbols, v], mode: d.symbols.length > 0 ? "pct" : d.mode })}
            size="sm"
            searchPlaceholder={t("admin.sim.overlay.searchPair")}
            emptyText={t("admin.sim.overlay.noPair")}
            trigger={
              <Button size="sm" variant="secondary" icon={<Plus size={14} />} data-testid="overlay-add-pair">
                {t("admin.sim.overlay.addPair")}
              </Button>
            }
            aria-label={t("admin.sim.overlay.addPair")}
          />
        )}
      </div>
      <div className="flex flex-wrap items-end gap-3">
        <Segmented
          size="sm"
          value={d.mode}
          onValueChange={(v) => set({ mode: v as OverlayDraft["mode"] })}
          items={[
            { value: "pct", label: t("admin.sim.overlay.byPct") },
            { value: "price", label: t("admin.sim.overlay.byPrice"), disabled: d.symbols.length > 1 },
          ]}
        />
        {d.mode === "pct" ? (
          <label className="flex w-36 flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.sim.overlay.pct")}
            <Input id="overlay-pct" size="sm" value={d.pct} inputMode="decimal" onValueChange={(v) => set({ pct: v })} className="font-mono" />
          </label>
        ) : (
          <label className="flex w-40 flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.sim.overlay.price")}
            <Input id="overlay-price" size="sm" value={d.price} inputMode="decimal" onValueChange={(v) => set({ price: v })} className="font-mono" />
          </label>
        )}
        {(["up", "hold", "down"] as const).map((k) => (
          <label key={k} className="flex w-28 flex-col gap-1.5 text-sm text-fg-2">
            {t(`admin.sim.overlay.${k}`)}
            <Input id={`overlay-${k}`} size="sm" value={d[k]} inputMode="numeric" onValueChange={(v) => set(secondsOf(k, v))} className="font-mono" />
          </label>
        ))}
        <span className={total > 600 ? "pb-2 text-xs text-danger-strong" : "pb-2 text-xs text-fg-3"}>{t("admin.sim.overlay.total", { s: total })}</span>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.sim.startsAt")}
          <input
            type="datetime-local"
            value={startsAt}
            onChange={(e) => setStartsAt(e.target.value)}
            className="h-8 rounded-1 border border-line-1 bg-bg-1 px-2 text-sm text-fg-1"
            aria-label={t("admin.sim.startsAt")}
          />
        </label>
      </div>
      <div className="flex flex-col gap-1">
        <Checkbox id="overlay-spare" checked={d.spare} onCheckedChange={(v) => set({ spare: v })} label={t("admin.sim.overlay.spare")} />
        <p className="text-xs text-fg-3" data-testid="overlay-risk-hint">
          {t("admin.sim.overlay.riskHint")}
        </p>
        {d.spare && <p className="text-xs text-warn-strong">{t("admin.sim.overlay.spareHint")}</p>}
        {platform && <p className="text-xs text-fg-3">{t("admin.sim.overlay.platformNote", { symbol: platform })}</p>}
      </div>
      {lines.length > 0 && <Lines lines={lines} />}
      <div className="flex flex-wrap items-center gap-3">
        <DangerAction
          trigger={(open) => (
            <Button onClick={open} disabled={d.symbols.length === 0} data-testid="overlay-start">
              {t("admin.sim.overlay.start")}
            </Button>
          )}
          danger={!d.spare || big}
          title={t("admin.sim.overlay.startTitle")}
          description={t("admin.sim.overlay.startHint")}
          target={
            <span className="flex flex-col gap-1">
              <Lines lines={lines} />
              <span className="text-xs text-fg-2">
                {t("admin.sim.overlay.seconds", { up: d.up, hold: d.hold, down: d.down })} ·{" "}
                {t(d.spare ? "admin.sim.overlay.spared" : "admin.sim.overlay.withRisk")}
              </span>
              {!d.spare && <span className="text-xs text-warn-strong">{t("admin.sim.overlay.riskHint")}</span>}
              {problem && <span className="text-xs text-danger-strong">{problem}</span>}
            </span>
          }
          confirmWord="overlay"
          disabled={!!problem}
          run={create}
          success={(res) =>
            (res as { approval?: unknown }).approval
              ? t("admin.sim.awaiting")
              : t("admin.sim.overlay.started", { n: (res as { items?: unknown[] }).items?.length ?? 0 })
          }
          invalidate={[simKey, simEventsKey]}
          onDone={() => setD({ ...d, symbols: [] })}
        />
        {problem && d.symbols.length > 0 && <span className="text-xs text-danger-strong">{problem}</span>}
      </div>
    </div>
  );
}

/** Lines shows each chosen pair from its price now to where the target takes it. */
function Lines({ lines }: { lines: { symbol: string; last?: string; to: string | null; move: number | null }[] }) {
  const { t } = useTranslation();
  return (
    <span className="grid grid-cols-[auto_auto_auto_auto] justify-start gap-x-4 gap-y-0.5 text-xs" data-testid="overlay-lines">
      {lines.map((l) => (
        <span key={l.symbol} className="contents">
          <span className="font-mono text-fg-1">{l.symbol}</span>
          <span className="font-mono text-fg-3">
            {t("admin.sim.overlay.now")} {l.last ? shown(l.last) : t("admin.sim.overlay.noPrice")}
          </span>
          <span className="font-mono text-fg-1">
            {t("admin.sim.overlay.to")} {shown(l.to)}
          </span>
          <span className="font-mono">{l.move === null ? "" : pct(l.move)}</span>
        </span>
      ))}
    </span>
  );
}
