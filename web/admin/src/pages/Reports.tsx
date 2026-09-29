import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin } from "../api/client";
import { Card, ErrorText, Field, Select, Table, time } from "../ui";

const ranges = ["7", "30", "90"] as const;
const intervals = ["1m", "5m", "15m", "1h", "4h", "1d"] as const;

export function ReportsPage(_: { admin: Admin }) {
  const [days, setDays] = useState<(typeof ranges)[number]>("7");
  const trading = useQuery({
    queryKey: ["reports", "trading", days],
    queryFn: async () => data(await api.GET("/admin/v1/reports/trading", { params: { query: { days: Number(days) } } })).items,
  });
  const wallet = useQuery({
    queryKey: ["reports", "wallet", days],
    queryFn: async () => data(await api.GET("/admin/v1/reports/wallet", { params: { query: { days: Number(days) } } })).items,
  });
  return (
    <>
      <Card
        title="交易（按交易对、UTC 日）"
        actions={
          <div className="w-32">
            <Select label="最近天数" options={ranges} value={days} onChange={(e) => setDays(e.target.value as (typeof ranges)[number])} />
          </div>
        }
      >
        <p className="mb-2 text-xs text-slate-500">来自 ClickHouse 读模型（成交与订单事件），比服务晚几秒。</p>
        <ErrorText text={trading.isError ? describe(trading.error) : undefined} />
        <Table
          head={["日期", "交易对", "成交笔数", "成交量", "成交额", "受理订单", "被拒订单"]}
          rows={(trading.data ?? []).map((d) => [d.day, d.symbol, d.trades, d.volume, d.quote_volume, d.orders, d.rejected])}
        />
      </Card>
      <Card title="充值与提现（按资产、UTC 日）">
        <ErrorText text={wallet.isError ? describe(wallet.error) : undefined} />
        <Table
          head={["日期", "资产", "入账充值笔数", "充值金额", "完成提现笔数", "提现金额", "提现手续费"]}
          rows={(wallet.data ?? []).map((d) => [d.day, d.asset, d.deposits, d.deposit_amount, d.withdrawals, d.withdrawal_amount, d.withdrawal_fees])}
        />
      </Card>
      <Candles />
    </>
  );
}

function Candles() {
  const [symbol, setSymbol] = useState("BTC-USDT");
  const [period, setPeriod] = useState<(typeof intervals)[number]>("1h");
  const candles = useQuery({
    queryKey: ["reports", "candles", symbol, period],
    queryFn: async () =>
      data(await api.GET("/admin/v1/reports/candles", { params: { query: { symbol, interval: period, limit: 48 } } })).items,
    enabled: symbol.trim() !== "",
  });
  return (
    <Card
      title="K 线（成交读模型）"
      actions={
        <div className="flex gap-2">
          <div className="w-36">
            <Field label="交易对" value={symbol} onChange={(e) => setSymbol(e.target.value.toUpperCase())} />
          </div>
          <div className="w-24">
            <Select label="周期" options={intervals} value={period} onChange={(e) => setPeriod(e.target.value as (typeof intervals)[number])} />
          </div>
        </div>
      }
    >
      <ErrorText text={candles.isError ? describe(candles.error) : undefined} />
      <Table
        head={["开盘时间", "开", "高", "低", "收", "成交量", "成交额", "笔数"]}
        rows={(candles.data ?? []).map((c) => [time(c.open_time), c.open, c.high, c.low, c.close, c.volume, c.quote_volume, c.trades])}
        empty="这个周期没有成交"
      />
    </Card>
  );
}
