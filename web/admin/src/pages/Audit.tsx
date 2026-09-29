import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, data, describe, type Admin } from "../api/client";
import { Button, Card, ErrorText, Field, Mono, Table, time } from "../ui";

type Filter = { actor: string; target: string };

// summary picks the readable fields of an audit payload.
function summary(payload: unknown): string {
  if (typeof payload !== "object" || payload === null) return "";
  const p = payload as Record<string, unknown>;
  return ["action", "reason", "details", "oldValue", "newValue"]
    .filter((k) => typeof p[k] === "string" && p[k] !== "")
    .map((k) => `${k}: ${String(p[k])}`)
    .join(" · ");
}

export function AuditPage(_: { admin: Admin }) {
  const [actor, setActor] = useState("");
  const [target, setTarget] = useState("");
  const [filter, setFilter] = useState<Filter>({ actor: "", target: "" });
  const list = useQuery({
    queryKey: ["audit", filter],
    queryFn: async () =>
      data(
        await api.GET("/admin/v1/audit-logs", {
          params: { query: { ...(filter.actor ? { actor: filter.actor } : {}), ...(filter.target ? { target: filter.target } : {}), limit: 200 } },
        }),
      ).items,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setFilter({ actor: actor.trim(), target: target.trim() });
  };
  return (
    <Card title="审计日志（最近 200 条，写入后几秒内可查）">
      <form className="mb-3 flex flex-wrap items-end gap-2" onSubmit={submit}>
        <div className="w-64">
          <Field label="操作者（管理员邮箱、cli:ubuntu…）" value={actor} onChange={(e) => setActor(e.target.value)} />
        </div>
        <div className="w-64">
          <Field label="对象（user:ID、pair:BTC-USDT、flag:KEY…）" value={target} onChange={(e) => setTarget(e.target.value)} />
        </div>
        <Button type="submit">查询</Button>
      </form>
      <ErrorText text={list.isError ? describe(list.error) : undefined} />
      <Table
        head={["时间", "事件", "操作者", "对象", "内容"]}
        rows={(list.data ?? []).map((e) => [
          <span key="t" className="whitespace-nowrap">
            {time(e.occurred_at)}
          </span>,
          <Mono key="ty">{e.event_type.replace(/^.*\./, "")}</Mono>,
          e.actor,
          <Mono key="ta">{e.target}</Mono>,
          <span key="p" className="text-xs break-all">
            {summary(e.payload)}
          </span>,
        ])}
      />
    </Card>
  );
}
