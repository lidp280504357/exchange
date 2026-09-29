import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin, type ContractState, type RiskPosition } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Mono, Notice, Select, Table, time } from "../ui";
import { nextStatus, statusTone } from "./Instruments";

const kinds = ["", "WARNING", "STARTED", "FILLED", "ADL"] as const;
const kindNames: Record<string, string> = { WARNING: "预警", STARTED: "接管", FILLED: "强平成交", ADL: "自动减仓" };
const ranges = ["7", "30", "90"] as const;

export function DerivativesPage({ admin }: { admin: Admin }) {
  return (
    <>
      <Contracts admin={admin} />
      <InsuranceFund admin={admin} />
      <Risk />
      <Liquidations />
    </>
  );
}

function Contracts({ admin }: { admin: Admin }) {
  const list = useQuery({
    queryKey: ["derivatives", "contracts"],
    queryFn: async () => data(await api.GET("/admin/v1/derivatives/contracts")).contracts,
    refetchInterval: 10_000,
  });
  const lift = can(admin, "derivatives.write");
  const edit = can(admin, "instruments.write");
  return (
    <Card title="永续合约">
      <p className="mb-2 text-xs text-slate-500">
        指数或标记价格失效时合约自动进入只减仓（风控事件 SystemDegraded），恢复后需人工确认解除；解除前先确认标记价格已是最新，否则几秒后会再次进入只减仓。
      </p>
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["合约", "状态", "只减仓", "标记价格", "持仓量", "持仓数", ...(lift || edit ? ["操作"] : [])]}
        rows={(list.data ?? []).map((c) => [
          <span key="s" className="font-medium">
            {c.symbol}
          </span>,
          <Badge key="st" tone={statusTone[c.status]}>
            {c.status}
          </Badge>,
          c.reduce_only ? (
            <div key="r" className="space-y-0.5">
              <Badge tone="red">只减仓</Badge>
              <div className="text-xs text-slate-500">
                {c.reduce_only_reason} · {time(c.reduce_only_since)}
              </div>
            </div>
          ) : (
            <div key="r" className="space-y-0.5">
              <Badge tone="green">正常</Badge>
              {c.lifted_by && <div className="text-xs text-slate-500">上次由 {c.lifted_by} 解除</div>}
            </div>
          ),
          <div key="m" className="space-y-0.5">
            <div>{c.mark_price ?? "—"}</div>
            {c.mark_price && <Badge tone={c.mark_fresh ? "green" : "red"}>{c.mark_fresh ? "最新" : `过期 ${time(c.mark_at)}`}</Badge>}
          </div>,
          c.open_interest,
          c.positions,
          ...(lift || edit
            ? [
                <div key="a" className="space-y-2">
                  {lift && c.reduce_only && <LiftReduceOnly contract={c} />}
                  {edit && <ContractStatus contract={c} />}
                </div>,
              ]
            : []),
        ])}
      />
    </Card>
  );
}

function LiftReduceOnly({ contract }: { contract: ContractState }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const lift = useMutation({
    mutationFn: async () =>
      data(
        await api.POST("/admin/v1/derivatives/contracts/{symbol}/lift-reduce-only", {
          params: { path: { symbol: contract.symbol } },
          body: { reason },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["derivatives", "contracts"] }),
  });
  return (
    <div className="w-56 space-y-1.5">
      <Field label="解除理由" value={reason} onChange={(e) => setReason(e.target.value)} />
      <Button disabled={lift.isPending || reason.trim().length < 3 || !contract.mark_fresh} onClick={() => lift.mutate()}>
        解除只减仓
      </Button>
      {!contract.mark_fresh && <p className="text-xs text-red-600">标记价格未恢复，不能解除</p>}
      <ErrorText text={lift.isError ? describe(lift.error) : undefined} />
      <Notice text={lift.isSuccess ? (lift.data.lifted ? "已解除" : "该合约本来就不是只减仓") : undefined} />
    </div>
  );
}

function ContractStatus({ contract }: { contract: ContractState }) {
  const qc = useQueryClient();
  const options = nextStatus[contract.status] ?? [];
  const [to, setTo] = useState(options[0] ?? "");
  const [reason, setReason] = useState("");
  const change = useMutation({
    mutationFn: async () =>
      data(
        await api.POST("/admin/v1/derivatives/contracts/{symbol}/status", {
          params: { path: { symbol: contract.symbol } },
          body: { to: to as ContractState["status"], reason },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["derivatives", "contracts"] }),
  });
  if (options.length === 0) return <span className="text-xs text-slate-400">已下线</span>;
  return (
    <div className="w-64 space-y-1.5">
      <div className="grid grid-cols-2 gap-2">
        <Select label="状态改为" options={options} value={to} onChange={(e) => setTo(e.target.value)} />
        <Field label="理由" value={reason} onChange={(e) => setReason(e.target.value)} />
      </div>
      {to === "CANCEL_ONLY" && <p className="text-xs text-red-600">CANCEL_ONLY 之后只能下线，不能恢复交易</p>}
      <Button variant={to === "TRADING" ? "primary" : "danger"} disabled={change.isPending || reason.trim().length < 3} onClick={() => change.mutate()}>
        确认
      </Button>
      <ErrorText text={change.isError ? describe(change.error) : undefined} />
      <Notice text={change.isSuccess ? `${change.data.from} → ${change.data.to}（合约服务约一分钟内生效）` : undefined} />
    </div>
  );
}

function InsuranceFund({ admin }: { admin: Admin }) {
  const qc = useQueryClient();
  const fund = useQuery({
    queryKey: ["derivatives", "insurance"],
    queryFn: async () => data(await api.GET("/admin/v1/derivatives/insurance-fund")),
    refetchInterval: 30_000,
  });
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const request = useMutation({
    mutationFn: async () =>
      data(await api.POST("/admin/v1/derivatives/insurance-fund/contributions", { body: { asset: "USDT", amount: amount.trim(), reason } })),
    onSuccess: () => {
      setAmount("");
      setReason("");
      return qc.invalidateQueries({ queryKey: ["approvals"] });
    },
  });
  const valid = /^\d+(\.\d+)?$/.test(amount.trim()) && Number(amount) > 0 && reason.trim().length >= 3;
  return (
    <Card title="保险基金">
      <ErrorText text={fund.isError ? describe(fund.error) : undefined} />
      {fund.data && (
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          <div>
            <div className="text-xs text-slate-500">余额（INSURANCE_FUND）</div>
            <div className="text-lg font-semibold">
              {fund.data.balance} {fund.data.asset}
            </div>
          </div>
          <div>
            <div className="text-xs text-slate-500">盈亏清算账户（PNL_CLEARING，未平仓位的未结算盈亏，可为负）</div>
            <div className="text-lg font-semibold">
              {fund.data.pnl_clearing} {fund.data.asset}
            </div>
          </div>
        </div>
      )}
      {can(admin, "ledger.adjust.request") && (
        <>
          <p className="mb-2 text-xs text-slate-500">
            注入模拟资金（对手方 ADJUSTMENT 系统账户），需另一位管理员在「双人审批」里批准后才记账，且要开着 ledger.manual_adjustment。
          </p>
          <div className="grid gap-3 sm:grid-cols-4">
            <Field label="金额（USDT）" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} />
            <div className="sm:col-span-2">
              <Field label="理由" value={reason} onChange={(e) => setReason(e.target.value)} />
            </div>
            <div className="flex items-end">
              <Button disabled={!valid || request.isPending} onClick={() => request.mutate()}>
                提交注资申请
              </Button>
            </div>
          </div>
          <div className="mt-2">
            <ErrorText text={request.isError ? describe(request.error) : undefined} />
            <Notice text={request.isSuccess ? `已提交，等待另一位管理员审批（${request.data.id}）` : undefined} />
          </div>
        </>
      )}
    </Card>
  );
}

function riskState(p: RiskPosition) {
  if (p.liquidating) return <Badge tone="red">强平接管中（第 {p.liquidation_attempts} 次）</Badge>;
  if (p.warned_at) return <Badge tone="yellow">已预警 {time(p.warned_at)}</Badge>;
  return <Badge>接近预警</Badge>;
}

function Risk() {
  const list = useQuery({
    queryKey: ["derivatives", "risk"],
    queryFn: async () => data(await api.GET("/admin/v1/derivatives/risk")).positions,
    refetchInterval: 5_000,
  });
  return (
    <Card title="强平监控（每 5 秒刷新）">
      <p className="mb-2 text-xs text-slate-500">保证金率 = 维持保证金 ÷（保证金 + 未实现盈亏），到 1 即接管；全仓仓位在这里按单个仓位计算，实际按整个全仓账户判断。</p>
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["用户", "合约", "数量", "模式", "标记价格", "保证金", "未实现盈亏", "维持保证金", "保证金率", "强平价", "状态"]}
        rows={(list.data ?? []).map((p) => [
          <Mono key="u">{p.user_id}</Mono>,
          `${p.symbol}${p.position_side === "BOTH" ? "" : ` ${p.position_side}`}`,
          <span key="q" className={p.quantity.startsWith("-") ? "text-red-600" : "text-emerald-700"}>
            {p.quantity}
          </span>,
          `${p.margin_mode === "CROSS" ? "全仓" : "逐仓"} ${p.leverage}x`,
          p.mark_price ?? "—",
          p.margin,
          p.unrealized_pnl ?? "—",
          p.maintenance_margin ?? "—",
          p.margin_ratio ?? "—",
          p.liquidation_price ?? "—",
          riskState(p),
        ])}
        empty="没有接近强平的仓位"
      />
    </Card>
  );
}

function Liquidations() {
  const [kind, setKind] = useState<(typeof kinds)[number]>("");
  const [days, setDays] = useState<(typeof ranges)[number]>("7");
  const list = useQuery({
    queryKey: ["derivatives", "liquidations", kind, days],
    queryFn: async () =>
      data(await api.GET("/admin/v1/derivatives/liquidations", { params: { query: { days: Number(days), limit: 200, ...(kind ? { kind } : {}) } } }))
        .items,
  });
  return (
    <Card
      title="强平记录（读模型）"
      actions={
        <div className="flex gap-2">
          <div className="w-28">
            <Select label="类型" options={kinds} value={kind} onChange={(e) => setKind(e.target.value as (typeof kinds)[number])} />
          </div>
          <div className="w-24">
            <Select label="最近天数" options={ranges} value={days} onChange={(e) => setDays(e.target.value as (typeof ranges)[number])} />
          </div>
        </div>
      }
    >
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["时间", "类型", "用户", "合约", "价格", "数量", "已实现盈亏", "保险基金垫付", "标记/破产价", "保证金余额/维持"]}
        rows={(list.data ?? []).map((l) => [
          time(l.occurred_at),
          <Badge key="k" tone={l.kind === "WARNING" ? "yellow" : l.kind === "ADL" ? "gray" : "red"}>
            {kindNames[l.kind] ?? l.kind}
            {l.kind === "FILLED" && l.adl ? "（ADL）" : ""}
          </Badge>,
          <Mono key="u">{l.user_id}</Mono>,
          l.symbol || (l.cross ? "全仓账户" : "—"),
          l.kind === "FILLED" || l.kind === "ADL" ? l.price : "—",
          l.quantity,
          l.realized_pnl,
          l.insurance_paid,
          l.kind === "STARTED" ? `${l.mark_price} / ${l.bankruptcy_price}` : "—",
          l.kind === "WARNING" || l.kind === "STARTED" ? `${l.margin_balance} / ${l.maintenance_margin}` : "—",
        ])}
        empty="这段时间没有强平"
      />
    </Card>
  );
}
