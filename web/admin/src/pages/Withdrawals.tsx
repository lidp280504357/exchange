import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin, type Withdrawal } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Mono, Notice, Select, Table, time } from "../ui";

const statuses = ["PENDING_REVIEW", "APPROVED", "SIGNING", "BROADCAST", "CONFIRMING", "CONFIRMED", "REJECTED", "CANCELED", "FAILED"] as const;

export function WithdrawalsPage({ admin }: { admin: Admin }) {
  const [status, setStatus] = useState<string>("PENDING_REVIEW");
  const list = useQuery({
    queryKey: ["withdrawals", status],
    queryFn: async () => data(await api.GET("/admin/v1/withdrawals", { params: { query: { status } } })).items,
    refetchInterval: 15_000,
  });
  const reviewer = can(admin, "withdrawals.review");
  return (
    <Card
      title="提现"
      actions={
        <div className="w-48">
          <Select label="状态" options={statuses} value={status} onChange={(e) => setStatus(e.target.value)} />
        </div>
      }
    >
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["申请时间", "用户", "金额", "地址", "折合 USDT", "风控", "审批", status === "PENDING_REVIEW" && reviewer ? "处理" : "状态"]}
        rows={(list.data ?? []).map((w) => [
          time(w.created_at),
          <Mono key="u">{w.user_id}</Mono>,
          <span key="a" className="whitespace-nowrap">
            {w.amount} {w.asset}
            <span className="block text-xs text-slate-500">
              手续费 {w.fee} · {w.network}
              {w.internal ? " · 站内" : ""}
            </span>
          </span>,
          <Mono key="addr">{w.address}</Mono>,
          w.value_usdt ?? "—",
          <span key="r" className="space-x-1">
            <Badge tone={(w.risk_score ?? 0) >= 50 ? "red" : (w.risk_score ?? 0) > 0 ? "yellow" : "gray"}>{w.risk_score ?? 0}</Badge>
            {w.risk_reasons.map((r) => (
              <Badge key={r}>{r}</Badge>
            ))}
          </span>,
          `${w.approvals?.length ?? 0}/${w.approvals_required}${w.approvals?.length ? `（${w.approvals.join("、")}）` : ""}`,
          status === "PENDING_REVIEW" && reviewer ? <Review key="x" w={w} /> : <Badge key="s">{w.status}</Badge>,
        ])}
      />
    </Card>
  );
}

function Review({ w }: { w: Withdrawal }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const review = useMutation({
    mutationFn: async (approve: boolean) =>
      data(await api.POST("/admin/v1/withdrawals/{id}/review", { params: { path: { id: w.id } }, body: { approve, reason } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["withdrawals"] }),
  });
  return (
    <div className="w-56 space-y-1.5">
      <Field label="理由（至少 3 个字）" value={reason} onChange={(e) => setReason(e.target.value)} />
      <div className="flex gap-2">
        <Button disabled={reason.trim().length < 3 || review.isPending} onClick={() => review.mutate(true)}>
          批准
        </Button>
        <Button variant="danger" disabled={reason.trim().length < 3 || review.isPending} onClick={() => review.mutate(false)}>
          拒绝
        </Button>
      </div>
      <ErrorText text={review.isError ? describe(review.error) : undefined} />
      <Notice text={review.isSuccess ? `已处理：${review.data.status}` : undefined} />
    </div>
  );
}
