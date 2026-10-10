import { LOCALES, type Locale } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Drawer, Input, KeyTag, Progress, Segmented, Switch, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Mail, Send } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError, lastFour } from "../../kit/actions";
import { Fields } from "../../kit/fields";
import { Num, TimeText, UserCell } from "../../kit/format";
import { ListTable, pageSize, useCursorList } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";

// Operators' in-app messages (design 2026-10-02 §4.5): to everyone, one
// user or the accounts with a tag, in Chinese and maybe English, with a
// path on the sites to open and maybe an email; delivered in rounds, each
// with how many have it and how many read it. A message whose rounds keep
// failing waits longer each time and, after ten, is FAILED until an
// operator resumes it (C5.5 ⑫).

type Broadcast = AdminSchemas["Broadcast"];
type Audience = "ALL" | "USER" | "TAG";

// A message's languages are the sites' (core's LOCALES, A99): Simplified
// Chinese required, Traditional (G7b) and English optional.
const LANGUAGE: Record<Locale, string> = { "zh-CN": "admin.content.zh", "zh-TW": "admin.content.zhTW", en: "admin.content.en" };
const LEGEND: Record<Locale, string> = { "zh-CN": "admin.broadcasts.chinese", "zh-TW": "admin.broadcasts.traditional", en: "admin.broadcasts.english" };

const broadcastsKey = ["admin", "broadcasts"];
const right: DataColumnMeta = { align: "right" };
const uuidRE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const tagRE = /^[A-Z][A-Z0-9_]{0,31}$/;
/** A path on the sites, never "//" (another host); as notification-service checks it. */
const linkRE = /^\/([A-Za-z0-9_\-.][A-Za-z0-9/_\-?=&.%]{0,199})?$/;
const TAGS = ["VIP", "TEST", "SUSPICIOUS"];

const readShare = (b: Broadcast) => (b.recipients > 0 ? Math.round((b.read / b.recipients) * 100) : 0);
const statusTone = { SENT: "success", SENDING: "info", FAILED: "danger" } as const;

export default function Broadcasts({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const list = useCursorList<Broadcast>(broadcastsKey, async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/broadcasts", { params: { query: { cursor, limit: pageSize() } } })),
  );
  // Counts move while a message is delivered and read: ask again meanwhile.
  const sending = list.rows.some((b) => b.status === "SENDING");
  const { refetch } = list.query;
  useEffect(() => {
    const id = setInterval(() => void refetch(), sending ? 5_000 : 30_000);
    return () => clearInterval(id);
  }, [sending, refetch]);
  const [composing, setComposing] = useState(false);
  const [open, setOpen] = useState<Broadcast | null>(null);
  const columns = useMemo<ColumnDef<Broadcast, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} style="datetime" /> },
      {
        id: "message", header: t("admin.broadcasts.message"),
        cell: ({ row: { original: b } }) => (
          <span className="flex max-w-[28rem] flex-col">
            <span className="flex items-center gap-1.5 font-medium text-fg-1">
              {b.title["zh-CN"]}
              {b.email && <Mail size={12} className="inline-block shrink-0 align-middle text-fg-3" aria-label={t("admin.broadcasts.email")} />}
            </span>
            <span className="truncate text-xs text-fg-3">{b.body["zh-CN"]}</span>
          </span>
        ),
      },
      {
        id: "audience", header: t("admin.broadcasts.audience"),
        cell: ({ row: { original: b } }) => (b.audience === "ALL" ? t("admin.broadcasts.everyone") : t("admin.broadcasts.users", { n: b.users })),
      },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row: { original: b } }) => (
          <span className="flex flex-col items-start gap-0.5">
            <Badge tone={statusTone[b.status]} dot={b.status === "SENDING"}>
              {t(`admin.broadcasts.status.${b.status}`)}
            </Badge>
            {b.status === "SENDING" && b.failures > 0 && <span className="text-xs text-warn-strong">{t("admin.broadcasts.retrying", { n: b.failures })}</span>}
          </span>
        ),
      },
      { id: "recipients", header: t("admin.broadcasts.recipients"), meta: right, cell: ({ row }) => <span className="tabular-nums">{row.original.recipients}</span> },
      {
        id: "read", header: t("admin.broadcasts.read"), meta: right,
        cell: ({ row: { original: b } }) => (
          <span className="tabular-nums">
            {b.read} <span className="text-xs text-fg-3">({readShare(b)}%)</span>
          </span>
        ),
      },
      { id: "by", header: t("admin.broadcasts.by"), cell: ({ row }) => <span className="text-xs">{row.original.created_by}</span> },
    ],
    [t],
  );
  return (
    <Page
      title={t("admin.nav.broadcasts")}
      help={t("admin.broadcasts.help")}
      actions={
        can(admin, "notices.send") && (
          <Button icon={<Send size={14} />} onClick={() => setComposing(true)} data-testid="broadcast-new">
            {t("admin.broadcasts.new")}
          </Button>
        )
      }
    >
      <Card className="stagger">
        <ListTable list={list} columns={columns} getRowId={(b) => b.id} onRowClick={setOpen} empty={t("admin.broadcasts.none")} aria-label={t("admin.nav.broadcasts")} />
      </Card>
      {composing && <Compose onClose={() => setComposing(false)} onSent={(b) => setOpen(b)} />}
      {open && <BroadcastDrawer admin={admin} id={open.id} initial={open} onClose={() => setOpen(null)} />}
    </Page>
  );
}

/**
 * BroadcastDrawer shows a message in both languages with its delivery,
 * asked again while it is sent; a FAILED one shows why and is resumed
 * with a reason.
 */
function BroadcastDrawer({ admin, id, initial, onClose }: { admin: Admin; id: string; initial: Broadcast; onClose: () => void }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: [...broadcastsKey, id],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/broadcasts/{id}", { params: { path: { id } } })),
    initialData: initial,
    refetchInterval: (query) => (query.state.data?.status === "SENDING" ? 3_000 : 15_000),
  });
  const b = q.data;
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={b.title["zh-CN"]}
      description={t(`admin.broadcasts.status.${b.status}`)}
      width={640}
      actions={
        b.status === "FAILED" &&
        can(admin, "notices.send") && (
          <DangerAction
            trigger={(open) => (
              <Button size="sm" onClick={open} data-testid="broadcast-resume">
                {t("admin.broadcasts.resume")}
              </Button>
            )}
            danger={false}
            title={t("admin.broadcasts.resumeTitle")}
            description={t("admin.broadcasts.resumeDesc")}
            target={<span>{b.title["zh-CN"]}</span>}
            confirmWord={lastFour(b.id)}
            run={async (reason) =>
              adminData(await adminApi.POST("/admin/v1/broadcasts/{id}/resume", { params: { path: { id: b.id } }, body: { reason } }))
            }
            success={t("admin.broadcasts.resumed")}
            invalidate={[broadcastsKey]}
          />
        )
      }
    >
      <div className="flex flex-col gap-4">
        {(b.status === "FAILED" || b.failures > 0) && (
          <div role="status" className="rounded-2 border border-danger/40 bg-danger/5 px-4 py-3 text-sm">
            <div className="font-medium text-danger-strong">
              {t(b.status === "FAILED" ? "admin.broadcasts.failedHint" : "admin.broadcasts.retryHint", { n: b.failures })}
            </div>
            {b.last_error && <div className="mt-1 break-all font-mono text-xs text-fg-2">{b.last_error}</div>}
            {b.status === "SENDING" && b.retry_at && (
              <div className="mt-1 text-xs text-fg-3">
                {t("admin.broadcasts.retryAt")} <TimeText value={b.retry_at} />
              </div>
            )}
          </div>
        )}
        <Fields
          label={t("admin.nav.broadcasts")}
          items={[
            { label: t("admin.broadcasts.audience"), value: b.audience === "ALL" ? t("admin.broadcasts.everyone") : t("admin.broadcasts.users", { n: b.users }) },
            { label: t("admin.broadcasts.recipients"), value: <Num value={String(b.recipients)} /> },
            {
              label: t("admin.broadcasts.read"),
              value: (
                <span>
                  <Num value={String(b.read)} /> <span className="text-fg-3">({readShare(b)}%)</span>
                </span>
              ),
            },
            { label: t("admin.broadcasts.link"), value: b.link ? <KeyTag>{b.link}</KeyTag> : "—", copy: b.link || undefined },
            { label: t("admin.broadcasts.email"), value: t(b.email ? "admin.common.yes" : "admin.common.no") },
            { label: t("admin.broadcasts.by"), value: b.created_by },
            { label: t("admin.common.createdAt"), value: <TimeText value={b.created_at} /> },
            { label: t("admin.broadcasts.finished"), value: b.finished_at ? <TimeText value={b.finished_at} /> : "—" },
          ]}
        />
        {b.audience === "USERS" && b.users > 0 && (
          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-fg-3">{t("admin.broadcasts.delivered", { n: b.recipients, of: b.users })}</span>
            <Progress value={Math.min(100, (b.recipients / b.users) * 100)} />
          </div>
        )}
        {LOCALES.map(
          (l) =>
            b.title[l] && (
              <section key={l} className="rounded-2 border border-line-1 p-4">
                <div className="mb-2 text-xs text-fg-3">{t(LANGUAGE[l])}</div>
                <h3 className="font-semibold text-fg-1">{b.title[l]}</h3>
                <p className="mt-1 whitespace-pre-wrap text-sm text-fg-2">{b.body[l]}</p>
              </section>
            ),
        )}
      </div>
    </Drawer>
  );
}

/** Compose writes a message and sends it after the confirmation. */
function Compose({ onClose, onSent }: { onClose: () => void; onSent: (b: Broadcast) => void }) {
  const { t } = useTranslation();
  const [audience, setAudience] = useState<Audience>("USER");
  const [userId, setUserId] = useState("");
  const [tag, setTag] = useState("");
  const [title, setTitle] = useState<Record<Locale, string>>({ "zh-CN": "", "zh-TW": "", en: "" });
  const [body, setBody] = useState<Record<Locale, string>>({ "zh-CN": "", "zh-TW": "", en: "" });
  const [link, setLink] = useState("");
  const [email, setEmail] = useState(false);
  const word = audience === "ALL" ? "all" : audience === "USER" ? lastFour(userId) || "user" : tag.toLowerCase() || "tag";
  const target =
    audience === "ALL" ? (
      t("admin.broadcasts.everyone")
    ) : audience === "USER" ? (
      uuidRE.test(userId.trim()) ? <UserCell id={userId.trim()} /> : "—"
    ) : (
      <span className="font-mono">{tag || "—"}</span>
    );
  const send = async (reason: string, key: string) => {
    const uid = userId.trim();
    if (audience === "USER" && !uuidRE.test(uid)) throw new FormError(t("admin.broadcasts.badUser"));
    if (audience === "TAG" && !tagRE.test(tag)) throw new FormError(t("admin.broadcasts.badTag"));
    if (!title["zh-CN"].trim() || !body["zh-CN"].trim()) throw new FormError(t("admin.broadcasts.needChinese"));
    if (!title.en.trim() !== !body.en.trim()) throw new FormError(t("admin.broadcasts.needBoth"));
    if (!title["zh-TW"].trim() !== !body["zh-TW"].trim()) throw new FormError(t("admin.broadcasts.needBothTW"));
    if (link && !linkRE.test(link)) throw new FormError(t("admin.broadcasts.badLink"));
    // The optional languages only when written: their readers get the Simplified otherwise.
    const written = (m: Record<Locale, string>) =>
      Object.fromEntries((["zh-TW", "en"] as const).filter((l) => m[l].trim()).map((l) => [l, m[l].trim()]));
    return adminData(
      await adminApi.POST("/admin/v1/broadcasts", {
        params: { header: { "Idempotency-Key": key } },
        body: {
          audience,
          ...(audience === "USER" ? { user_id: uid } : {}),
          ...(audience === "TAG" ? { tag } : {}),
          title: { "zh-CN": title["zh-CN"].trim(), ...written(title) },
          body: { "zh-CN": body["zh-CN"].trim(), ...written(body) },
          ...(link ? { link } : {}),
          email,
          reason,
        },
      }),
    );
  };
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("admin.broadcasts.new")}
      description={t("admin.broadcasts.composeHelp")}
      width={720}
      actions={
        <DangerAction
          trigger={(open) => (
            <Button size="sm" icon={<Send size={12} />} onClick={open} data-testid="broadcast-send">
              {t("admin.broadcasts.send")}
            </Button>
          )}
          danger={audience === "ALL"}
          title={t("admin.broadcasts.sendTitle")}
          description={t(audience === "ALL" ? "admin.broadcasts.sendAll" : "admin.broadcasts.sendSome")}
          target={target}
          confirmWord={word}
          run={send}
          success={t("admin.broadcasts.sent")}
          invalidate={[broadcastsKey]}
          onDone={(b) => {
            onClose();
            onSent(b as Broadcast);
          }}
        />
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.broadcasts.audience")}
          <Segmented
            value={audience}
            onValueChange={(v) => setAudience(v as Audience)}
            items={(["USER", "TAG", "ALL"] as const).map((a) => ({ value: a, label: t(`admin.broadcasts.to.${a}`) }))}
            aria-label={t("admin.broadcasts.audience")}
          />
        </div>
        {audience === "USER" && (
          <label className="flex flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.broadcasts.userId")}
            <Input
              id="broadcast-user"
              value={userId}
              onValueChange={setUserId}
              autoComplete="off"
              placeholder="0190…"
              error={userId.trim() && !uuidRE.test(userId.trim()) ? t("admin.broadcasts.badUser") : undefined}
            />
          </label>
        )}
        {audience === "TAG" && (
          <label className="flex flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.broadcasts.tag")}
            <Input
              id="broadcast-tag"
              value={tag}
              onValueChange={(v) => setTag(v.toUpperCase())}
              autoComplete="off"
              error={tag && !tagRE.test(tag) ? t("admin.broadcasts.badTag") : undefined}
              hint={t("admin.broadcasts.tagHint")}
            />
            <span className="flex flex-wrap gap-1.5">
              {TAGS.map((x) => (
                <button key={x} type="button" onClick={() => setTag(x)} className="rounded-1 border border-line-1 px-2 py-0.5 font-mono text-xs hover:border-brand">
                  {x}
                </button>
              ))}
            </span>
          </label>
        )}
        {audience === "ALL" && <p className="rounded-2 bg-warn/10 px-3 py-2 text-sm text-warn-strong">{t("admin.broadcasts.allWarning")}</p>}
        {LOCALES.map((l) => (
          <fieldset key={l} className="flex flex-col gap-3 rounded-2 border border-line-1 p-4">
            <legend className="px-1 text-xs text-fg-3">{t(LEGEND[l])}</legend>
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.content.title")}
              <Input id={`broadcast-title-${l}`} value={title[l]} maxLength={100} onValueChange={(v) => setTitle({ ...title, [l]: v })} />
            </label>
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.broadcasts.body")}
              <textarea
                id={`broadcast-body-${l}`}
                value={body[l]}
                onChange={(e) => setBody({ ...body, [l]: e.target.value })}
                rows={4}
                maxLength={2000}
                className="w-full rounded-2 border border-line-1 bg-bg-1 px-3 py-2 text-sm text-fg-1 outline-none focus-visible:border-brand"
              />
            </label>
          </fieldset>
        ))}
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.broadcasts.link")}
          <Input
            id="broadcast-link"
            value={link}
            onValueChange={setLink}
            placeholder="/assets"
            autoComplete="off"
            error={link && !linkRE.test(link) ? t("admin.broadcasts.badLink") : undefined}
            hint={t("admin.broadcasts.linkHint")}
          />
        </label>
        <Switch checked={email} onCheckedChange={setEmail} label={t("admin.broadcasts.alsoEmail")} description={t("admin.broadcasts.emailHint")} />
      </div>
    </Drawer>
  );
}
