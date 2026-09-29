import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin, type Approval } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Mono, Notice, Select, Table, time } from "../ui";

const statuses = ["PENDING", "EXECUTED", "REJECTED", "FAILED", ""] as const;
const tone = { PENDING: "yellow", EXECUTED: "green", REJECTED: "gray", FAILED: "red" } as const;

export function LedgerPage({ admin }: { admin: Admin }) {
  const [status, setStatus] = useState<(typeof statuses)[number]>("PENDING");
  const list = useQuery({
    queryKey: ["approvals", status],
    queryFn: async () => data(await api.GET("/admin/v1/approvals", { params: { query: status ? { status } : {} } })).items,
  });
  return (
    <>
      {can(admin, "ledger.adjust.request") && <RequestForm />}
      <Card
        title="调账与保险基金注资申请（双人审批）"
        actions={
          <div className="w-40">
            <Select label="状态" options={statuses} value={status} onChange={(e) => setStatus(e.target.value as (typeof statuses)[number])} />
          </div>
        }
      >
        <ErrorText text={list.isError ? describe(list.error) : undefined} />
        <Table
          head={["申请时间", "类型", "对象", "金额", "理由", "发起人", "状态", "结果", can(admin, "ledger.adjust.approve") ? "审批" : "处理时间"]}
          keys={(list.data ?? []).map((a) => a.id)}
          rows={(list.data ?? []).map((a) => [
            time(a.created_at),
            a.kind === "INSURANCE_FUND" ? "保险基金注资" : "手动调账",
            a.kind === "INSURANCE_FUND" ? "保险基金" : <Mono key="u">{a.payload.user_id}</Mono>,
            <span key="m" className="whitespace-nowrap">
              {a.payload.amount} {a.payload.asset}
            </span>,
            a.reason,
            <span key="r" className="text-xs">
              {a.requested_by === admin.id ? "我" : <Mono>{a.requested_by}</Mono>}
            </span>,
            <Badge key="s" tone={tone[a.status]}>
              {a.status}
            </Badge>,
            <span key="res" className="text-xs">
              {a.result || "—"}
            </span>,
            a.status === "PENDING" && can(admin, "ledger.adjust.approve") ? <Decide key="d" approval={a} self={a.requested_by === admin.id} /> : time(a.decided_at),
          ])}
        />
      </Card>
    </>
  );
}

function RequestForm() {
  const qc = useQueryClient();
  const [userId, setUserId] = useState("");
  const [asset, setAsset] = useState("USDT");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const request = useMutation({
    mutationFn: async () =>
      data(await api.POST("/admin/v1/ledger/adjustments", { body: { user_id: userId.trim(), asset, amount: amount.trim(), reason } })),
    onSuccess: () => {
      setAmount("");
      setReason("");
      return qc.invalidateQueries({ queryKey: ["approvals"] });
    },
  });
  const valid = /^-?\d+(\.\d+)?$/.test(amount.trim()) && Number(amount) !== 0 && reason.trim().length >= 3 && userId.trim().length > 0;
  return (
    <Card title="发起手动调账">
      <p className="mb-2 text-xs text-slate-500">正数给用户现货账户加钱、负数扣钱（对手方 ADJUSTMENT 系统账户），需另一位管理员批准后才记账。</p>
      <div className="grid gap-3 sm:grid-cols-5">
        <div className="sm:col-span-2">
          <Field label="用户 ID" value={userId} onChange={(e) => setUserId(e.target.value)} />
        </div>
        <Field label="资产" value={asset} onChange={(e) => setAsset(e.target.value.toUpperCase())} />
        <Field label="金额（可为负）" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} />
        <Field label="理由" value={reason} onChange={(e) => setReason(e.target.value)} />
      </div>
      <div className="mt-3 flex items-center gap-3">
        <Button disabled={!valid || request.isPending} onClick={() => request.mutate()}>
          提交申请
        </Button>
        <ErrorText text={request.isError ? describe(request.error) : undefined} />
        <Notice text={request.isSuccess ? `已提交，等待另一位管理员审批（${request.data.id}）` : undefined} />
      </div>
    </Card>
  );
}

function Decide({ approval, self }: { approval: Approval; self: boolean }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const decide = useMutation({
    mutationFn: async (approve: boolean) =>
      data(await api.POST("/admin/v1/approvals/{id}/decide", { params: { path: { id: approval.id } }, body: { approve, reason } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["approvals"] }),
  });
  if (self) return <span className="text-xs text-slate-400">需另一位管理员审批</span>;
  return (
    <div className="w-56 space-y-1.5">
      <Field label="审批意见" value={reason} onChange={(e) => setReason(e.target.value)} />
      <div className="flex gap-2">
        <Button disabled={reason.trim().length < 3 || decide.isPending} onClick={() => decide.mutate(true)}>
          批准并记账
        </Button>
        <Button variant="ghost" disabled={reason.trim().length < 3 || decide.isPending} onClick={() => decide.mutate(false)}>
          驳回
        </Button>
      </div>
      <ErrorText text={decide.isError ? describe(decide.error) : undefined} />
    </div>
  );
}
