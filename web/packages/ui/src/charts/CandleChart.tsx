import { dec, formatDecimal, formatPercent, formatPrice, formatTime, type CandleData } from "@exchange/core";
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
  TickMarkType,
  createChart,
  type IChartApi,
  type ISeriesApi,
  type LogicalRange,
  type MouseEventParams,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import { LoaderCircle, Maximize2, Minimize2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { IconButton } from "../components/IconButton";
import { cn } from "../lib/cn";
import { useFormatContext } from "../lib/settings";
import { intervalParts, legendRoom, topMargin, updateMode, type CandleDataState, type LegendRoom, type UpdateMode } from "./candles";
import { createIndicatorClient, type IndicatorClient } from "./indicatorClient";
import type { IndicatorResult } from "./indicators";
import { numToDecimal } from "./numbers";
import { useChartTheme, type ChartTheme } from "./theme";

// The candle chart (design §4.4, §6.2) on lightweight-charts v5. The chart
// is created once: new candles go in with setData (history, a new symbol
// or interval) or series.update (the live candle), and paging older
// candles keeps the view where it was. Moving averages are computed in a
// Web Worker. Colours come from the tokens, times are drawn in the user's
// time zone.

export type Indicator = "MA" | "EMA" | "VOL";

export type CandleChartProps = {
  /** Candles oldest first (the last one may still be open). */
  candles: CandleData[];
  /** The interval shown ("1m" … "1M"). */
  interval: string;
  priceDecimals: number;
  /** Changing it (a new pair) redraws from scratch even if the times match. */
  symbol?: string;
  /** Fired when the view nears the oldest candle: load the page before it. */
  onLoadMore?: (oldestOpenTime: string) => void;
  /** false: the history is complete, stop asking. */
  hasMore?: boolean;
  loading?: boolean;
  /** Intervals offered in the toolbar, with onIntervalChange. */
  intervals?: string[];
  onIntervalChange?: (interval: string) => void;
  /** The indicators shown (controlled) or their start (default MA and VOL). */
  indicators?: Indicator[];
  defaultIndicators?: Indicator[];
  onIndicatorsChange?: (indicators: Indicator[]) => void;
  /** Fixed at mount. */
  maPeriods?: number[];
  emaPeriods?: number[];
  /**
   * Height of the plot (default 420 px). "fill" gives the plot whatever the
   * root's height leaves under the toolbar (size the root with className);
   * fullscreen fills the screen.
   */
  height?: number | string;
  toolbar?: boolean;
  className?: string;
};

// The toolbar's buttons are 44 px touch targets on touch screens (a ring
// around them would be cut off by the scrolling toolbar).
const TOUCH = "pointer-coarse:min-h-tap pointer-coarse:min-w-tap";

const LINE_CLASS = ["text-chart-1", "text-chart-2", "text-chart-3", "text-chart-4", "text-chart-5"];
const DEFAULT_MA = [7, 25, 99];
const DEFAULT_EMA = [12, 26];

const toTime = (iso: string) => Math.floor(Date.parse(iso) / 1000) as UTCTimestamp;
const num = (v: string) => dec.toNumber(v);

function timeToMs(t: Time): number {
  if (typeof t === "number") return t * 1000;
  if (typeof t === "string") return Date.parse(t);
  return Date.UTC(t.year, t.month - 1, t.day);
}

function candleBar(c: CandleData) {
  return { time: toTime(c.open_time), open: num(c.open), high: num(c.high), low: num(c.low), close: num(c.close) };
}

function volumeBar(c: CandleData, theme: ChartTheme) {
  return { time: toTime(c.open_time), value: num(c.volume), color: num(c.close) >= num(c.open) ? theme.upSoft : theme.downSoft };
}

function linePoint(time: UTCTimestamp, v: number | null | undefined) {
  return v === null || v === undefined ? { time } : { time, value: v };
}

/**
 * CandleChart draws candles with volume and MA/EMA lines, a legend that
 * follows the crosshair, interval and indicator switches, and a
 * fullscreen button. Load it lazily (import "@exchange/ui/charts/CandleChart")
 * so the chart library stays out of the first screen's bundle.
 */
export function CandleChart({
  candles, interval, priceDecimals, symbol = "", onLoadMore, hasMore, loading, intervals, onIntervalChange, indicators, defaultIndicators,
  onIndicatorsChange, maPeriods = DEFAULT_MA, emaPeriods = DEFAULT_EMA, height = 420, toolbar = true, className,
}: CandleChartProps) {
  const { t } = useTranslation();
  const { locale, timeZone } = useFormatContext();
  const theme = useChartTheme();
  const rootRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<HTMLDivElement>(null);
  const legendRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeRef = useRef<ISeriesApi<"Histogram"> | null>(null);
  const maRef = useRef<ISeriesApi<"Line">[]>([]);
  const emaRef = useRef<ISeriesApi<"Line">[]>([]);
  const clientRef = useRef<IndicatorClient | null>(null);
  const dataRef = useRef<CandleDataState | null>(null);
  const linesRef = useRef<{ key: string; len: number } | null>(null);
  const tokenRef = useRef(0);
  const candlesRef = useRef(candles);
  candlesRef.current = candles;
  const periodsRef = useRef({ ma: maPeriods, ema: emaPeriods });

  const [innerInd, setInnerInd] = useState<Indicator[]>(defaultIndicators ?? ["MA", "VOL"]);
  const shown = indicators ?? innerInd;
  const showMA = shown.includes("MA");
  const showEMA = shown.includes("EMA");
  const showVOL = shown.includes("VOL");
  const [hover, setHover] = useState<number | null>(null);
  const [lines, setLines] = useState<IndicatorResult | null>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [pseudoFull, setPseudoFull] = useState(false);

  // Room for the legend at the top of the candles' scale (topMargin, B116),
  // for the legend's tallest since the symbol, interval, indicators or
  // width last changed (legendRoom); refitted whenever the legend or the
  // plot changes size.
  const fitKey = `${symbol}|${interval}|${shown.join(",")}`;
  const fitKeyRef = useRef(fitKey);
  fitKeyRef.current = fitKey;
  const volRef = useRef(showVOL);
  volRef.current = showVOL;
  const roomRef = useRef<LegendRoom>({ key: "", width: 0, legend: 0 });
  const fitRef = useRef({ top: 0, vol: showVOL });
  const fit = useCallback(() => {
    const chart = chartRef.current;
    const candle = candleRef.current;
    const plot = plotRef.current;
    if (!chart || !candle || !plot) return;
    const room = legendRoom(roomRef.current, fitKeyRef.current, plot.clientWidth, legendRef.current?.offsetHeight ?? 0);
    roomRef.current = room;
    const top = topMargin(room.legend, plot.clientHeight - chart.timeScale().height());
    const f = fitRef.current;
    if (top === f.top && f.vol === volRef.current) return;
    f.top = top;
    f.vol = volRef.current;
    candle.priceScale().applyOptions({ scaleMargins: { top, bottom: volRef.current ? 0.24 : 0.06 } });
    plot.dataset.legendRoom = String(Math.round(top * 1000) / 1000);
  }, []);

  const toggleIndicator = (i: Indicator) => {
    const next = shown.includes(i) ? shown.filter((x) => x !== i) : [...shown, i];
    if (indicators === undefined) setInnerInd(next);
    onIndicatorsChange?.(next);
  };

  // Create the chart once; everything else updates it.
  useEffect(() => {
    const el = plotRef.current;
    if (!el) return;
    const th = theme;
    const chart = createChart(el, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: th.background },
        textColor: th.text,
        fontSize: 11,
        fontFamily: th.fontFamily,
        panes: { separatorColor: th.border },
        // The licence's link to TradingView is in the site footer (PC) and
        // the help page (mobile) instead of a logo on every chart.
        attributionLogo: false,
      },
      grid: { vertLines: { color: th.grid }, horzLines: { color: th.grid } },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: { color: th.crosshair, labelBackgroundColor: th.labelBackground },
        horzLine: { color: th.crosshair, labelBackgroundColor: th.labelBackground },
      },
      rightPriceScale: { borderColor: th.border },
      timeScale: { borderColor: th.border, timeVisible: true, secondsVisible: false, rightOffset: 8, barSpacing: 8 },
    });
    const candle = chart.addSeries(CandlestickSeries, {
      upColor: th.up,
      downColor: th.down,
      wickUpColor: th.up,
      wickDownColor: th.down,
      borderVisible: false,
    });
    candle.priceScale().applyOptions({ scaleMargins: { top: topMargin(0, 0), bottom: 0.24 } });
    const volume = chart.addSeries(HistogramSeries, { priceScaleId: "vol", priceFormat: { type: "volume" }, lastValueVisible: false, priceLineVisible: false });
    volume.priceScale().applyOptions({ scaleMargins: { top: 0.8, bottom: 0 } });
    const line = (color: string | undefined) =>
      chart.addSeries(LineSeries, { color, lineWidth: 1, priceLineVisible: false, lastValueVisible: false, crosshairMarkerVisible: false });
    maRef.current = periodsRef.current.ma.map((_, i) => line(th.lines[i % th.lines.length]));
    emaRef.current = periodsRef.current.ema.map((_, i) => line(th.lines[(i + 3) % th.lines.length]));

    const onCrosshair = (param: MouseEventParams<Time>) => {
      setHover(param.logical === undefined || param.time === undefined ? null : Math.round(param.logical));
    };
    chart.subscribeCrosshairMove(onCrosshair);

    chartRef.current = chart;
    candleRef.current = candle;
    volumeRef.current = volume;
    clientRef.current = createIndicatorClient();
    return () => {
      chart.unsubscribeCrosshairMove(onCrosshair);
      clientRef.current?.dispose();
      clientRef.current = null;
      chart.remove();
      chartRef.current = null;
      candleRef.current = null;
      volumeRef.current = null;
      maRef.current = [];
      emaRef.current = [];
      dataRef.current = null;
      linesRef.current = null;
    };
    // The chart is built once; the effects below follow theme, data and options.
  }, []);

  // Paging: near the left edge, ask once per oldest candle for the page before it.
  const loadRef = useRef(onLoadMore);
  loadRef.current = onLoadMore;
  const hasMoreRef = useRef(hasMore);
  hasMoreRef.current = hasMore;
  const askedRef = useRef<string | null>(null);
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    const onRange = (range: LogicalRange | null) => {
      if (!range || range.from > 10) return;
      const oldest = candlesRef.current[0]?.open_time;
      if (!oldest || hasMoreRef.current === false || askedRef.current === oldest || !loadRef.current) return;
      askedRef.current = oldest;
      loadRef.current(oldest);
    };
    chart.timeScale().subscribeVisibleLogicalRangeChange(onRange);
    return () => chart.timeScale().unsubscribeVisibleLogicalRangeChange(onRange);
  }, []);

  // Theme: colours of the chart, the candles and the volume bars.
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    chart.applyOptions({
      layout: { background: { type: ColorType.Solid, color: theme.background }, textColor: theme.text, panes: { separatorColor: theme.border } },
      grid: { vertLines: { color: theme.grid }, horzLines: { color: theme.grid } },
      crosshair: {
        vertLine: { color: theme.crosshair, labelBackgroundColor: theme.labelBackground },
        horzLine: { color: theme.crosshair, labelBackgroundColor: theme.labelBackground },
      },
      rightPriceScale: { borderColor: theme.border },
      timeScale: { borderColor: theme.border },
    });
    candleRef.current?.applyOptions({ upColor: theme.up, downColor: theme.down, wickUpColor: theme.up, wickDownColor: theme.down });
    maRef.current.forEach((s, i) => s.applyOptions({ color: theme.lines[i % theme.lines.length] }));
    emaRef.current.forEach((s, i) => s.applyOptions({ color: theme.lines[(i + 3) % theme.lines.length] }));
    volumeRef.current?.setData(candlesRef.current.map((c) => volumeBar(c, theme)));
  }, [theme]);

  // Times in the user's language and zone, on the axis and the crosshair.
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    const parts = intervalParts(interval);
    const daily = parts !== null && parts.unit !== "m" && parts.unit !== "h";
    chart.applyOptions({
      localization: {
        locale,
        timeFormatter: (time: Time) => formatTime(timeToMs(time), daily ? "date" : "datetime", locale, timeZone),
      },
      timeScale: {
        timeVisible: !daily,
        tickMarkFormatter: (time: Time, type: TickMarkType) => {
          const ms = timeToMs(time);
          const date = formatTime(ms, "date", locale, timeZone);
          if (type === TickMarkType.Year) return date.slice(0, 4);
          if (type === TickMarkType.Month) return date.slice(0, 7);
          if (type === TickMarkType.DayOfMonth) return date.slice(5);
          return formatTime(ms, type === TickMarkType.Time ? "time" : "timeSeconds", locale, timeZone);
        },
      },
    });
  }, [locale, timeZone, interval]);

  useEffect(() => {
    candleRef.current?.applyOptions({ priceFormat: { type: "price", precision: priceDecimals, minMove: 1 / 10 ** priceDecimals } });
  }, [priceDecimals]);

  // The moving averages: computed off the main thread, drawn by setData or,
  // for the live candle, by updating the last point.
  const runIndicators = useCallback(
    (list: CandleData[], mode: UpdateMode, key: string) => {
      const client = clientRef.current;
      if (!client || list.length === 0 || (!showMA && !showEMA)) return;
      const token = ++tokenRef.current;
      const times = list.map((c) => toTime(c.open_time));
      void client.compute({ closes: list.map((c) => num(c.close)), ma: periodsRef.current.ma, ema: periodsRef.current.ema }).then((res) => {
        if (token !== tokenRef.current || !chartRef.current) return;
        const n = times.length;
        const prev = linesRef.current;
        const tail = (mode === "tail" || mode === "append") && prev !== null && prev.key === key && prev.len >= n - 1;
        const apply = (series: ISeriesApi<"Line">[], values: (number | null)[][]) =>
          series.forEach((s, i) => {
            const v = values[i] ?? [];
            if (tail) {
              if (mode === "append" && n >= 2) s.update(linePoint(times[n - 2] as UTCTimestamp, v[n - 2]));
              s.update(linePoint(times[n - 1] as UTCTimestamp, v[n - 1]));
            } else {
              s.setData(times.map((tm, j) => linePoint(tm, v[j])));
            }
          });
        apply(maRef.current, res.ma);
        apply(emaRef.current, res.ema);
        linesRef.current = { key, len: n };
        setLines(res);
      });
    },
    [showMA, showEMA],
  );

  // New candles: reset, page, append or update in place.
  useEffect(() => {
    const chart = chartRef.current;
    const candle = candleRef.current;
    const volume = volumeRef.current;
    if (!chart || !candle || !volume) return;
    const key = `${symbol}|${interval}`;
    const first = candles[0];
    const last = candles[candles.length - 1];
    if (!first || !last) {
      candle.setData([]);
      volume.setData([]);
      [...maRef.current, ...emaRef.current].forEach((s) => s.setData([]));
      dataRef.current = null;
      linesRef.current = null;
      setLines(null);
      return;
    }
    const mode = updateMode(dataRef.current, key, candles);
    if (mode === "tail" || mode === "append") {
      const prevLast = candles[candles.length - 2];
      if (mode === "append" && prevLast) {
        candle.update(candleBar(prevLast));
        volume.update(volumeBar(prevLast, theme));
      }
      candle.update(candleBar(last));
      volume.update(volumeBar(last, theme));
    } else {
      const range = mode === "prepend" ? chart.timeScale().getVisibleLogicalRange() : null;
      candle.setData(candles.map(candleBar));
      volume.setData(candles.map((c) => volumeBar(c, theme)));
      if (mode === "prepend" && range && dataRef.current) {
        const added = candles.length - dataRef.current.len;
        chart.timeScale().setVisibleLogicalRange({ from: range.from + added, to: range.to + added });
      } else {
        chart.timeScale().resetTimeScale();
        askedRef.current = null;
      }
    }
    dataRef.current = { key, firstTime: first.open_time, firstOpen: first.open, lastTime: last.open_time, len: candles.length };
    runIndicators(candles, mode, `${key}|${first.open_time}`);
    // theme is read, not followed: the theme effect repaints volume bars.
  }, [candles, interval, symbol, runIndicators]);

  // Indicator switches: visibility, and a full computation when turned on.
  useEffect(() => {
    maRef.current.forEach((s) => s.applyOptions({ visible: showMA }));
    emaRef.current.forEach((s) => s.applyOptions({ visible: showEMA }));
    volumeRef.current?.applyOptions({ visible: showVOL });
    fit();
    const list = candlesRef.current;
    const d = dataRef.current;
    if ((showMA || showEMA) && d && list[0]) {
      linesRef.current = null;
      runIndicators(list, "reset", `${d.key}|${list[0].open_time}`);
    }
  }, [showMA, showEMA, showVOL, runIndicators, fit]);

  // The legend and the plot change size: refit the room at the top.
  useEffect(() => {
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => fit());
    if (legendRef.current) observer.observe(legendRef.current);
    if (plotRef.current) observer.observe(plotRef.current);
    return () => observer.disconnect();
  }, [fit]);
  // A new symbol, interval or set of indicators measures the legend afresh.
  useEffect(() => fit(), [fitKey, fit]);

  // Fullscreen: the Fullscreen API where there is one, else a fixed overlay (iOS).
  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === rootRef.current);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);
  const toggleFullscreen = async () => {
    const el = rootRef.current;
    if (!el) return;
    if (document.fullscreenElement) {
      await document.exitFullscreen().catch(() => {});
      return;
    }
    if (pseudoFull) {
      setPseudoFull(false);
      return;
    }
    if (typeof el.requestFullscreen === "function") {
      try {
        await el.requestFullscreen();
        return;
      } catch {
        // fall back to the overlay
      }
    }
    setPseudoFull(true);
  };
  const isFull = fullscreen || pseudoFull;

  const unit = intervalParts(interval)?.unit;
  const daily = unit === "d" || unit === "w" || unit === "M";
  const idx = hover !== null && hover >= 0 && hover < candles.length ? hover : candles.length - 1;
  const cur = candles[idx];
  const change = cur && dec.isDecimal(cur.open) && dec.sign(cur.open) > 0 ? dec.div(dec.sub(cur.close, cur.open), cur.open, 6) : null;
  const intervalLabel = (i: string) => {
    const p = intervalParts(i);
    return p ? t(`ui.chart.intervals.${p.unit}`, { n: p.n }) : i;
  };

  return (
    <div
      ref={rootRef}
      className={cn(
        "flex min-w-0 flex-col bg-bg-1",
        pseudoFull && "fixed inset-0 z-[var(--z-dialog)]",
        isFull && "h-full w-full",
        className,
      )}
    >
      {toolbar && (
        <div className="flex h-9 shrink-0 items-center gap-1 overflow-x-auto border-b border-line-1 px-2 text-xs [scrollbar-width:none] pointer-coarse:h-auto">
          {intervals && intervals.length > 0 && (
            <div role="group" aria-label={t("ui.chart.interval")} className="flex items-center gap-0.5">
              {intervals.map((i) => (
                <button
                  key={i}
                  type="button"
                  aria-pressed={i === interval}
                  onClick={() => onIntervalChange?.(i)}
                  className={cn("h-6 rounded-1 px-2 whitespace-nowrap transition-colors", TOUCH, i === interval ? "bg-bg-3 text-fg-1" : "text-fg-3 hover:text-fg-1")}
                >
                  {intervalLabel(i)}
                </button>
              ))}
            </div>
          )}
          <span aria-hidden className="mx-1 h-4 w-px bg-line-1" />
          <div role="group" aria-label={t("ui.chart.indicators")} className="flex items-center gap-0.5">
            {(["MA", "EMA", "VOL"] as const).map((i) => (
              <button
                key={i}
                type="button"
                aria-pressed={shown.includes(i)}
                onClick={() => toggleIndicator(i)}
                className={cn("h-6 rounded-1 px-2 transition-colors", TOUCH, shown.includes(i) ? "text-brand" : "text-fg-3 hover:text-fg-1")}
              >
                {i}
              </button>
            ))}
          </div>
          <IconButton
            className="ml-auto pointer-coarse:size-tap"
            size="xs"
            icon={isFull ? <Minimize2 /> : <Maximize2 />}
            label={isFull ? t("ui.chart.exitFullscreen") : t("ui.chart.fullscreen")}
            onClick={() => void toggleFullscreen()}
          />
        </div>
      )}
      {/* A fixed height must not be a flex item that grows from 0: in a root without a height it would collapse. */}
      <div
        className={cn("relative min-h-0", isFull || height === "fill" ? "flex-1" : "shrink-0")}
        style={isFull || height === "fill" ? undefined : { height }}
      >
        <div
          ref={legendRef}
          data-testid="candle-legend"
          className="pointer-events-none absolute left-1 top-1.5 z-10 flex max-w-[calc(100%-76px)] flex-col gap-0.5 rounded-1 bg-bg-1/70 px-1 text-xs tabular-nums"
        >
          {cur && (
            <>
              <div className="flex flex-wrap gap-x-2 text-fg-3">
                <span className="text-fg-2">{formatTime(cur.open_time, daily ? "date" : "datetime", locale, timeZone)}</span>
                {(
                  [
                    ["open", cur.open],
                    ["high", cur.high],
                    ["low", cur.low],
                    ["close", cur.close],
                  ] as const
                ).map(([k, v]) => (
                  <span key={k}>
                    {t(`ui.chart.${k}`)} <span className={num(cur.close) >= num(cur.open) ? "text-up" : "text-down"}>{formatPrice(v, priceDecimals)}</span>
                  </span>
                ))}
                <span>
                  {t("ui.chart.change")} <span className={change && dec.sign(change) < 0 ? "text-down" : "text-up"}>{formatPercent(change)}</span>
                </span>
                {showVOL && (
                  <span>
                    {t("ui.chart.volume")} <span className="text-fg-2">{formatDecimal(cur.volume, { decimals: 2 })}</span>
                  </span>
                )}
              </div>
              {lines && (showMA || showEMA) && (
                <div className="flex flex-wrap gap-x-2">
                  {showMA &&
                    maPeriods.map((p, i) => (
                      <span key={`ma${p}`} className={LINE_CLASS[i % LINE_CLASS.length]}>
                        MA{p} {formatPrice(numToDecimal(lines.ma[i]?.[idx] ?? NaN, priceDecimals), priceDecimals)}
                      </span>
                    ))}
                  {showEMA &&
                    emaPeriods.map((p, i) => (
                      <span key={`ema${p}`} className={LINE_CLASS[(i + 3) % LINE_CLASS.length]}>
                        EMA{p} {formatPrice(numToDecimal(lines.ema[i]?.[idx] ?? NaN, priceDecimals), priceDecimals)}
                      </span>
                    ))}
                </div>
              )}
            </>
          )}
        </div>
        <div ref={plotRef} data-testid="candle-plot" className="absolute inset-0" />
        {loading && (
          <div className="absolute inset-0 z-10 grid place-items-center bg-bg-1/40">
            <LoaderCircle size={20} className="animate-spin text-brand" aria-label={t("common.loading")} />
          </div>
        )}
        {!loading && candles.length === 0 && (
          <div className="absolute inset-0 z-10 grid place-items-center text-sm text-fg-3">{t("ui.chart.noData")}</div>
        )}
      </div>
    </div>
  );
}
