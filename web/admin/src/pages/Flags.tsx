import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, data, describe, type Admin, type Flag } from "../api/client";
import { can } from "../session";
import { Badge, Button, Card, ErrorText, Field, Mono, Table, time } from "../ui";

export function FlagsPage({ admin }: { admin: Admin }) {
  const list = useQuery({ queryKey: ["flags"], queryFn: async () => data(await api.GET("/admin/v1/flags")).items });
  const editable = can(admin, "flags.write");
  return (
    <Card title="功能开关（全站紧急开关）">
      <p className="mb-2 text-xs text-slate-500">
        开关默认关闭；切换只改启用状态，保留地区、账户状态与白名单规则（规则用 exchangectl flags set 修改）。各服务 5 秒内生效，变更写审计。
      </p>
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["开关", "状态", "说明", "规则", "最近修改", editable ? "切换" : "版本"]}
        rows={(list.data ?? []).map((f) => [
          <Mono key="k">{f.key}</Mono>,
          f.enabled ? <Badge tone="green">开</Badge> : <Badge>关</Badge>,
          <span key="d" className="text-xs">
            {f.description}
          </span>,
          <Mono key="r">{f.rules && JSON.stringify(f.rules) !== "{}" ? JSON.stringify(f.rules) : "—"}</Mono>,
          <span key="u" className="text-xs whitespace-nowrap">
            {f.updated_by || "—"}
            <span className="block text-slate-500">
              {time(f.updated_at)} · v{f.version}
            </span>
          </span>,
          editable ? <Switch key="s" flag={f} /> : `v${f.version}`,
        ])}
      />
    </Card>
  );
}

function Switch({ flag }: { flag: Flag }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const change = useMutation({
    mutationFn: async () =>
      data(await api.PUT("/admin/v1/flags/{key}", { params: { path: { key: flag.key } }, body: { enabled: !flag.enabled, reason } })),
    onSuccess: () => {
      setReason("");
      return qc.invalidateQueries({ queryKey: ["flags"] });
    },
  });
  return (
    <div className="w-56 space-y-1.5">
      <Field label="理由（至少 3 个字）" value={reason} onChange={(e) => setReason(e.target.value)} />
      <Button variant={flag.enabled ? "danger" : "primary"} disabled={change.isPending || reason.trim().length < 3} onClick={() => change.mutate()}>
        {flag.enabled ? "关闭" : "开启"}
      </Button>
      <ErrorText text={change.isError ? describe(change.error) : undefined} />
    </div>
  );
}
