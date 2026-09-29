import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin, type Pair } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Notice, Select, Table } from "../ui";

// Appendix B: PREPARE → TRADING ⇄ HALT; TRADING or HALT → CANCEL_ONLY → DELISTED.
export const nextStatus: Record<string, string[]> = {
  PREPARE: ["TRADING"],
  TRADING: ["HALT", "CANCEL_ONLY"],
  HALT: ["TRADING", "CANCEL_ONLY"],
  CANCEL_ONLY: ["DELISTED"],
  DELISTED: [],
};

export const statusTone = { TRADING: "green", HALT: "yellow", CANCEL_ONLY: "red", DELISTED: "gray", PREPARE: "gray" } as const;

function on(v?: boolean) {
  return v ? <Badge tone="green">开</Badge> : <Badge tone="red">关</Badge>;
}

export function InstrumentsPage({ admin }: { admin: Admin }) {
  const list = useQuery({ queryKey: ["instruments"], queryFn: async () => data(await api.GET("/admin/v1/instruments")) });
  const editable = can(admin, "instruments.write");
  return (
    <>
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Card title="交易对（单交易对紧急开关）">
        <Table
          head={["交易对", "状态", "最小下单量", "价格步长", "数量步长", "费率 maker/taker", editable ? "改状态" : "版本"]}
          keys={(list.data?.pairs ?? []).map((p) => p.symbol ?? "")}
          rows={(list.data?.pairs ?? []).map((p) => [
            <span key="s" className="font-medium">
              {p.symbol}
            </span>,
            <Badge key="st" tone={statusTone[p.status ?? "PREPARE"]}>
              {p.status}
            </Badge>,
            p.min_quantity,
            p.tick_size,
            p.lot_size,
            `${p.maker_fee_rate} / ${p.taker_fee_rate}`,
            editable ? <PairStatus key="e" pair={p} /> : `v${p.version}`,
          ])}
        />
      </Card>
      <Card title="永续合约（参数）">
        <p className="mb-2 text-xs text-slate-500">合约参数同样来自参考数据文件；合约状态、只减仓与保险基金在「合约」页处理。</p>
        <Table
          head={["合约", "状态", "指数", "最小下单量", "价格步长", "最高杠杆", "资金费（间隔、利率、上限）", "费率 maker/taker"]}
          rows={(list.data?.contracts ?? []).map((c) => [
            <span key="s" className="font-medium">
              {c.symbol}
            </span>,
            <Badge key="st" tone={statusTone[c.status ?? "PREPARE"]}>
              {c.status}
            </Badge>,
            c.index_symbol,
            c.min_quantity,
            c.tick_size,
            `${c.risk_tiers?.[0]?.max_leverage ?? "—"}x`,
            `${c.funding_interval_hours}h · ${c.interest_rate} · ±${c.funding_cap}`,
            `${c.maker_fee_rate} / ${c.taker_fee_rate}`,
          ])}
        />
      </Card>
      <Card title="资产与网络">
        <p className="mb-2 text-xs text-slate-500">
          资产与网络参数以仓库里的参考数据文件为准（deploy/instruments/test.json），每次部署幂等同步；要改请改文件后部署。临时停止某资产的充提用功能开关或交易对状态。
        </p>
        <Table
          head={["资产", "精度", "充值", "提现", "交易", "网络（确认数、最小充值、提现费、充/提）"]}
          rows={(list.data?.assets ?? []).map((a) => [
            <span key="c" className="font-medium">
              {a.asset_code} <span className="text-xs text-slate-500">{a.name}</span>
            </span>,
            a.decimals,
            on(a.deposit_enabled),
            on(a.withdraw_enabled),
            on(a.trading_enabled),
            <div key="n" className="space-y-1">
              {(a.networks ?? []).map((n) => (
                <div key={n.network} className="text-xs">
                  {n.network}：{n.confirmations} 确认 · 最小 {n.min_deposit} · 费 {n.withdraw_fee} · {on(n.deposit_enabled)} {on(n.withdraw_enabled)}
                </div>
              ))}
            </div>,
          ])}
        />
      </Card>
    </>
  );
}

function PairStatus({ pair }: { pair: Pair }) {
  const qc = useQueryClient();
  const options = nextStatus[pair.status ?? ""] ?? [];
  const [to, setTo] = useState(options[0] ?? "");
  const [reason, setReason] = useState("");
  const change = useMutation({
    mutationFn: async () =>
      data(
        await api.POST("/admin/v1/instruments/pairs/{symbol}/status", {
          params: { path: { symbol: pair.symbol ?? "" } },
          body: { to: to as NonNullable<Pair["status"]>, reason },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instruments"] }),
  });
  if (options.length === 0) return <span className="text-xs text-slate-400">已下线</span>;
  return (
    <div className="w-64 space-y-1.5">
      <div className="grid grid-cols-2 gap-2">
        <Select label="改为" options={options} value={to} onChange={(e) => setTo(e.target.value)} />
        <Field label="理由" value={reason} onChange={(e) => setReason(e.target.value)} />
      </div>
      {to === "CANCEL_ONLY" && <p className="text-xs text-red-600">CANCEL_ONLY 之后只能下线，不能恢复交易</p>}
      <Button variant={to === "TRADING" ? "primary" : "danger"} disabled={change.isPending || reason.trim().length < 3} onClick={() => change.mutate()}>
        确认
      </Button>
      <ErrorText text={change.isError ? describe(change.error) : undefined} />
      <Notice text={change.isSuccess ? `${change.data.from} → ${change.data.to}` : undefined} />
    </div>
  );
}
