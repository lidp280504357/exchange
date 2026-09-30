// @exchange/ui: the design system every app shares (design §5, ADR-0012).
export * from "./i18n";
export * from "./lib/cn";
export * from "./lib/motion";
export * from "./components/Button";
export * from "./components/Spinner";
export * from "./components/Skeleton";
export * from "./components/States";
export * from "./components/PriceText";

// Helpers apps may reuse.
export * from "./lib/identity";
export * from "./lib/clipboard";
export { useNow, subscribeClock, clockNow } from "./lib/clock";
export * from "./lib/useFlash";
export * from "./lib/settings";

// Base and feedback components.
export * from "./components/IconButton";
export * from "./components/Input";
export * from "./components/NumberInput";
export * from "./components/Slider";
export * from "./components/Select";
export * from "./components/Combobox";
export * from "./components/Checkbox";
export * from "./components/Switch";
export * from "./components/RadioGroup";
export * from "./components/Segmented";
export * from "./components/Tabs";
export * from "./components/Badge";
export * from "./components/Tooltip";
export * from "./components/Popover";
export * from "./components/DropdownMenu";
export * from "./components/Dialog";
export * from "./components/ConfirmDialog";
export * from "./components/Sheet";
export * from "./components/Toast";
export * from "./components/Progress";
export * from "./components/Stepper";
export * from "./components/Avatar";
export * from "./components/CoinIcon";

// Data display.
export * from "./data/DataTable";
export * from "./data/CopyButton";
export * from "./data/KeyValue";
export * from "./data/Stat";
export * from "./data/Sparkline";
export * from "./data/AmountText";
export * from "./data/TimeText";
export * from "./data/CountUp";
export * from "./data/Marquee";
export * from "./data/DepthBars";

// Trading.
export * from "./trading/orderMath";
export * from "./trading/OrderForm";
export * from "./trading/OrderBook";
export * from "./trading/TradeTape";
export * from "./trading/PositionCard";
export * from "./trading/FundingCountdown";
export * from "./trading/LeverageDialog";
export * from "./trading/tpsl";
export * from "./trading/TpSlDialog";

// Charts. CandleChart here is the lazy one (lightweight-charts in its own
// chunk); "@exchange/ui/charts/CandleChart" is the eager component.
export { CandleChart } from "./charts/LazyCandleChart";
export type { CandleChartProps, Indicator } from "./charts/CandleChart";
export * from "./charts/candles";
export * from "./charts/DepthChart";
export * from "./charts/indicators";
export * from "./charts/theme";
export * from "./charts/numbers";

// Forms.
export * from "./form/Form";
