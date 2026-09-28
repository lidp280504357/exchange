import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authApi, notificationApi, unwrap, userApi } from "../api/client";
import { StepUp } from "../components/StepUp";
import { Badge, Button, Card, ErrorText, Field, Notice, Time } from "../components/ui";
import { codeText, errorText, setLanguage } from "../i18n";
import { useSession } from "../store/session";

export function NotificationsPage() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const list = useInfiniteQuery({
    queryKey: ["notifications"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(notificationApi.GET("/v1/notifications", { params: { query: { limit: 20, cursor: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const markAll = useMutation({
    mutationFn: () => unwrap(notificationApi.POST("/v1/notifications/read", { body: { all: true } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notifications"] }),
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const unread = list.data?.pages[0]?.unread_count ?? 0;
  return (
    <Card
      title={<>{t("notices.title")} {unread > 0 && <Badge tone="yellow">{t("notices.unread", { n: unread })}</Badge>}</>}
      actions={<Button variant="ghost" disabled={unread === 0} onClick={() => markAll.mutate()}>{t("notices.markAll")}</Button>}
    >
      {items.length === 0 ? <p className="text-sm text-gray-500">{t("common.none")}</p> : (
        <ul className="divide-y divide-white/5">
          {items.map((n) => (
            <li key={n.id} className="py-3">
              <div className="flex items-center gap-2 text-sm font-medium">
                {!n.read && <span className="h-2 w-2 rounded-full bg-[#f0b90b]" />}
                {n.title}
              </div>
              <p className="mt-1 text-sm text-gray-400">{n.body}</p>
              <div className="mt-1 text-xs text-gray-500"><Time value={n.created_at} /></div>
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && <Button variant="ghost" className="mt-3" onClick={() => list.fetchNextPage()}>{t("common.more")}</Button>}
    </Card>
  );
}

export function SecurityPage() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const current = useSession((s) => s.session?.sessionId);
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: () => unwrap(authApi.GET("/v1/auth/sessions")) });
  const history = useQuery({ queryKey: ["login-history"], queryFn: () => unwrap(authApi.GET("/v1/auth/login-history", { params: { query: { limit: 20 } } })) });
  const [pending, setPending] = useState<null | { kind: "one"; id: string } | { kind: "others" }>(null);
  const [stepUpOpen, setStepUpOpen] = useState(false);
  const [error, setError] = useState("");

  async function act(token: string) {
    setStepUpOpen(false);
    setError("");
    const headers = { "X-Step-Up-Token": token };
    try {
      if (pending?.kind === "one") {
        await unwrap(authApi.DELETE("/v1/auth/sessions/{session_id}", { params: { path: { session_id: pending.id }, header: headers } }));
      } else if (pending?.kind === "others") {
        await unwrap(authApi.POST("/v1/auth/logout/all", { params: { header: headers } }));
      }
      void qc.invalidateQueries({ queryKey: ["sessions"] });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(null);
    }
  }

  return (
    <div className="space-y-4">
      <Card
        title={t("security.sessions")}
        actions={<Button variant="danger" onClick={() => { setPending({ kind: "others" }); setStepUpOpen(true); }}>{t("security.revokeOthers")}</Button>}
      >
        <ErrorText text={error || (sessions.error ? errorText(sessions.error) : "")} />
        <ul className="divide-y divide-white/5 text-sm">
          {(sessions.data?.sessions ?? []).map((s) => (
            <li key={s.session_id} className="flex items-center justify-between gap-2 py-2">
              <div className="min-w-0">
                <div className="truncate">{s.user_agent || s.device_id} {s.session_id === current && <Badge tone="green">{t("security.current")}</Badge>}</div>
                <div className="text-xs text-gray-500">{codeText(s.client_type)} · {s.ip} · <Time value={s.last_seen_at} /></div>
              </div>
              {s.session_id !== current && (
                <Button variant="ghost" onClick={() => { setPending({ kind: "one", id: s.session_id }); setStepUpOpen(true); }}>{t("security.revoke")}</Button>
              )}
            </li>
          ))}
        </ul>
      </Card>
      <Card title={t("security.history")}>
        <ul className="divide-y divide-white/5 text-sm">
          {(history.data?.items ?? []).map((h, i) => (
            <li key={i} className="flex items-center justify-between py-2">
              <div>
                <div>{codeText(h.method)} <Badge tone={h.result === "SUCCESS" ? "green" : "red"}>{codeText(h.result)}</Badge> {h.new_device && <Badge tone="yellow">{t("security.newDevice")}</Badge>}</div>
                <div className="text-xs text-gray-500">{h.identity} · {h.ip}</div>
              </div>
              <div className="text-xs text-gray-500"><Time value={h.created_at} /></div>
            </li>
          ))}
        </ul>
      </Card>
      {stepUpOpen && <StepUp onToken={act} onCancel={() => { setStepUpOpen(false); setPending(null); }} />}
    </div>
  );
}

export function SettingsPage() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const profile = useQuery({ queryKey: ["profile"], queryFn: () => unwrap(userApi.GET("/v1/user/profile")) });
  const [code, setCode] = useState<string | null>(null);
  const [stepUpOpen, setStepUpOpen] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");

  async function patch(body: { language?: string; timezone?: string; anti_phishing_code?: string }, stepUp?: string) {
    setError("");
    setSaved("");
    try {
      await unwrap(userApi.PATCH("/v1/user/profile", { body, params: { header: stepUp ? { "X-Step-Up-Token": stepUp } : {} } }));
      setSaved(t("profile.saved"));
      void qc.invalidateQueries({ queryKey: ["profile"] });
    } catch (e) {
      setError(errorText(e));
    }
  }

  const p = profile.data;
  return (
    <Card title={t("profile.title")}>
      <div className="space-y-4 text-sm">
        {p && <div>{t("profile.status")}: <Badge tone={p.status === "ACTIVE" ? "green" : "red"}>{codeText(p.status, "status")}</Badge> · {p.region}</div>}
        <label className="block space-y-1">
          <span className="text-xs text-gray-400">{t("profile.language")}</span>
          <select
            className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2"
            value={i18n.language}
            onChange={(e) => { setLanguage(e.target.value); void patch({ language: e.target.value }); }}
          >
            <option value="zh-CN">简体中文</option>
            <option value="en">English</option>
          </select>
        </label>
        {p && <div className="text-xs text-gray-400">{t("profile.timezone")}: {p.timezone}</div>}
        <Field
          label={t("profile.antiPhishing")}
          hint={t("profile.antiPhishingHint")}
          value={code ?? p?.anti_phishing_code ?? ""}
          maxLength={20}
          onChange={(e) => setCode(e.target.value)}
        />
        <Button disabled={code === null} onClick={() => setStepUpOpen(true)}>{t("profile.save")}</Button>
        <ErrorText text={error} />
        <Notice text={saved} />
      </div>
      {stepUpOpen && (
        <StepUp
          onToken={(tok) => { setStepUpOpen(false); void patch({ anti_phishing_code: code ?? "" }, tok).then(() => setCode(null)); }}
          onCancel={() => setStepUpOpen(false)}
        />
      )}
    </Card>
  );
}
