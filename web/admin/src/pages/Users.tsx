import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, data, describe, type Admin, type UserView } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Mono, Notice, Select, Table, time } from "../ui";

const targets = ["FROZEN", "ACTIVE", "RISK_REVIEW", "CLOSED"] as const;
const reasonCodes = ["SUSPICIOUS_LOGIN", "FRAUD_SUSPECTED", "COMPLIANCE_REVIEW", "USER_REQUEST", "REVIEW_CLEARED"];

const statusTone = { ACTIVE: "green", RISK_REVIEW: "yellow", FROZEN: "red", CLOSED: "gray" } as const;

export function UsersPage({ admin }: { admin: Admin }) {
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const user = useQuery({
    queryKey: ["user", query],
    queryFn: async () => data(await api.GET("/admin/v1/users/lookup", { params: { query: { q: query } } })),
    enabled: query !== "",
    retry: false,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setQuery(input.trim());
  };
  return (
    <>
      <Card title="查找用户">
        <form className="flex flex-wrap items-end gap-2" onSubmit={submit}>
          <div className="min-w-72 flex-1">
            <Field label="用户 ID、邮箱或手机号（+86…）" name="q" value={input} onChange={(e) => setInput(e.target.value)} />
          </div>
          <Button type="submit" disabled={!input.trim()}>
            查找
          </Button>
        </form>
      </Card>
      {user.isError && <ErrorText text={describe(user.error)} />}
      {user.data && <UserCard admin={admin} view={user.data} />}
    </>
  );
}

function UserCard({ admin, view }: { admin: Admin; view: UserView }) {
  const u = view.user;
  return (
    <>
      <Card title="账户">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
          <Item label="用户 ID" value={<Mono>{u.id}</Mono>} />
          <Item label="状态" value={<Badge tone={statusTone[u.status]}>{u.status}</Badge>} />
          <Item label="注册时间" value={time(u.created_at)} />
          <Item label="地区" value={u.region || "—"} />
          <Item label="语言" value={u.language} />
          <Item label="KYC 等级" value={u.kyc_level} />
        </dl>
      </Card>
      <Card title="余额">
        <Table
          head={["账户", "资产", "可用", "冻结"]}
          rows={view.balances.map((b) => [b.account_type, b.asset, b.available, b.frozen])}
          empty="没有余额"
        />
      </Card>
      {can(admin, "users.status") && <StatusForm userId={u.id} />}
      {can(admin, "orders.cancel") && <CancelOrders userId={u.id} />}
    </>
  );
}

function Item({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-slate-500">{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function StatusForm({ userId }: { userId: string }) {
  const qc = useQueryClient();
  const [to, setTo] = useState<(typeof targets)[number]>("FROZEN");
  const [reason, setReason] = useState("SUSPICIOUS_LOGIN");
  const [note, setNote] = useState("");
  const change = useMutation({
    mutationFn: async () =>
      data(await api.POST("/admin/v1/users/{id}/status", { params: { path: { id: userId } }, body: { to, reason, note } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["user"] }),
  });
  return (
    <Card title="修改账户状态">
      <div className="grid gap-3 sm:grid-cols-4">
        <Select label="改为" options={targets} value={to} onChange={(e) => setTo(e.target.value as (typeof targets)[number])} />
        <div>
          <Field label="原因代码（大写）" list="reason-codes" value={reason} onChange={(e) => setReason(e.target.value.toUpperCase())} />
          <datalist id="reason-codes">
            {reasonCodes.map((c) => (
              <option key={c} value={c} />
            ))}
          </datalist>
        </div>
        <div className="sm:col-span-2">
          <Field label="备注（可选，工单号等）" value={note} onChange={(e) => setNote(e.target.value)} />
        </div>
      </div>
      <div className="mt-3 flex items-center gap-3">
        <Button variant={to === "ACTIVE" ? "primary" : "danger"} disabled={change.isPending || !reason} onClick={() => change.mutate()}>
          确认修改
        </Button>
        <ErrorText text={change.isError ? describe(change.error) : undefined} />
        <Notice text={change.isSuccess ? `已从 ${change.data.from} 改为 ${change.data.to}` : undefined} />
      </div>
    </Card>
  );
}

function CancelOrders({ userId }: { userId: string }) {
  const [reason, setReason] = useState("");
  const cancel = useMutation({
    mutationFn: async () => {
      const res = await api.POST("/admin/v1/users/{id}/cancel-orders", { params: { path: { id: userId } }, body: { reason } });
      if (!res.response.ok) data(res);
    },
  });
  return (
    <Card title="强制撤销全部挂单">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-72 flex-1">
          <Field label="理由（至少 3 个字）" value={reason} onChange={(e) => setReason(e.target.value)} />
        </div>
        <Button variant="danger" disabled={cancel.isPending || reason.trim().length < 3} onClick={() => cancel.mutate()}>
          撤销全部挂单
        </Button>
      </div>
      <div className="mt-2">
        <ErrorText text={cancel.isError ? describe(cancel.error) : undefined} />
        <Notice text={cancel.isSuccess ? "已提交撤单，撮合引擎处理完成后订单变为已撤销" : undefined} />
      </div>
    </Card>
  );
}
