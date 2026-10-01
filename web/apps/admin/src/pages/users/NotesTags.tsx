import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Input, Skeleton, toast } from "@exchange/ui";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { errorToast } from "../../kit/actions";
import { useTimeText } from "../../kit/format";

type Note = AdminSchemas["Note"];

const COMMON = ["VIP", "SUSPICIOUS", "TEST"];

/** TagChips shows an account's tags with their names. */
export function TagChips({ tags }: { tags: string[] }) {
  const { t } = useTranslation();
  if (tags.length === 0) return <span className="text-sm text-fg-3">{t("admin.user.noTags")}</span>;
  return (
    <span className="flex flex-wrap gap-1.5">
      {tags.map((tag) => (
        <Badge key={tag} tone={tag === "SUSPICIOUS" ? "danger" : tag === "VIP" ? "brand" : tag === "TEST" ? "info" : "neutral"} title={tag}>
          {t(`admin.user.tagNames.${tag}`, { defaultValue: tag })}
        </Badge>
      ))}
    </span>
  );
}

/** TagsEditor replaces an account's tags: remove one, add a common one or any code. */
export function TagsEditor({ admin, userId, tags }: { admin: Admin; userId: string; tags: string[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [draft, setDraft] = useState("");
  const save = useMutation({
    mutationFn: async (next: string[]) =>
      adminData(await adminApi.PUT("/admin/v1/users/{id}/tags", { params: { path: { id: userId } }, body: { tags: next } })),
    onSuccess: () => {
      toast.success(t("admin.user.tagsSaved"));
      void qc.invalidateQueries({ queryKey: ["admin", "user", userId, "detail"] });
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
    onError: (err) => errorToast(err),
  });
  const editable = can(admin, "users.notes");
  const add = (tag: string) => {
    const code = tag.trim().toUpperCase();
    if (code && !tags.includes(code)) save.mutate([...tags, code]);
    setDraft("");
  };
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {tags.length === 0 && <span className="text-sm text-fg-3">{t("admin.user.noTags")}</span>}
        {tags.map((tag) => (
          <span key={tag} className="inline-flex items-center gap-1 rounded-full border border-line-2 py-0.5 pl-2.5 pr-1 text-sm">
            {t(`admin.user.tagNames.${tag}`, { defaultValue: tag })}
            {editable && (
              <button
                type="button"
                aria-label={`${t("admin.common.reset")} ${tag}`}
                disabled={save.isPending}
                onClick={() => save.mutate(tags.filter((x) => x !== tag))}
                className="grid size-5 place-items-center rounded-full text-fg-3 hover:bg-bg-2 hover:text-fg-1"
              >
                <X size={12} />
              </button>
            )}
          </span>
        ))}
      </div>
      {editable && (
        <div className="flex flex-wrap items-center gap-2">
          {COMMON.filter((c) => !tags.includes(c)).map((c) => (
            <Button key={c} size="sm" variant="ghost" icon={<Plus size={14} />} disabled={save.isPending} onClick={() => add(c)}>
              {t(`admin.user.tagNames.${c}`)}
            </Button>
          ))}
          <Input
            size="sm"
            value={draft}
            onValueChange={(v) => setDraft(v.toUpperCase().replace(/[^A-Z0-9_]/g, ""))}
            onKeyDown={(e) => e.key === "Enter" && add(draft)}
            placeholder={t("admin.user.addTag")}
            maxLength={32}
            containerClassName="w-40"
            aria-label={t("admin.user.addTag")}
          />
          <span className="text-xs text-fg-3">{t("admin.user.tagsHint")}</span>
        </div>
      )}
    </div>
  );
}

/** Notes is the administrators' timeline on an account, with a box to add to it. */
export function Notes({ admin, userId }: { admin: Admin; userId: string }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const qc = useQueryClient();
  const [body, setBody] = useState("");
  const key = ["admin", "user", userId, "notes"];
  const list = useInfiniteQuery({
    queryKey: key,
    queryFn: async ({ pageParam }) =>
      adminData(await adminApi.GET("/admin/v1/users/{id}/notes", { params: { path: { id: userId }, query: { cursor: pageParam, limit: 20 } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const add = useMutation({
    mutationFn: async () =>
      adminData(await adminApi.POST("/admin/v1/users/{id}/notes", { params: { path: { id: userId } }, body: { body: body.trim() } })),
    onSuccess: () => {
      setBody("");
      toast.success(t("admin.user.noteAdded"));
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (err) => errorToast(err),
  });
  const notes: Note[] = list.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <div className="flex flex-col gap-4">
      {can(admin, "users.notes") && (
        <div className="flex flex-col gap-2">
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={3}
            maxLength={2000}
            placeholder={t("admin.user.notePlaceholder")}
            aria-label={t("admin.user.notes")}
            className="w-full resize-y rounded-2 border border-line-1 bg-bg-2 px-3 py-2 text-sm text-fg-1 outline-none placeholder:text-fg-3 focus-visible:border-brand focus-visible:ring-1 focus-visible:ring-brand"
          />
          <Button size="sm" className="self-end" disabled={!body.trim()} loading={add.isPending} onClick={() => add.mutate()}>
            {t("admin.user.addNote")}
          </Button>
        </div>
      )}
      {list.isPending ? (
        <Skeleton className="h-16 w-full" />
      ) : notes.length === 0 ? (
        <p className="py-6 text-center text-sm text-fg-3">{t("admin.user.noNotes")}</p>
      ) : (
        <ol className="relative flex flex-col gap-4 border-l border-line-1 pl-5">
          {notes.map((n, i) => (
            <li key={n.id} className="stagger relative" style={{ "--i": Math.min(i, 11) } as React.CSSProperties}>
              <span aria-hidden className="absolute -left-[25px] top-1.5 size-2.5 rounded-full border-2 border-bg-1 bg-brand" />
              <div className="text-xs text-fg-3">{t("admin.user.noteBy", { who: n.admin_email || n.admin_id.slice(0, 8), time: time(n.created_at) })}</div>
              <p className="mt-1 whitespace-pre-wrap break-words text-sm text-fg-1">{n.body}</p>
            </li>
          ))}
        </ol>
      )}
      {list.hasNextPage && (
        <Button size="sm" variant="ghost" className="self-center" loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
          {t("admin.user.more")}
        </Button>
      )}
    </div>
  );
}
