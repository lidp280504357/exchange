import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Avatar, Badge, ErrorState, Skeleton, Tabs } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link, useParams, useSearchParams } from "react-router";
import { EnumBadge } from "../../kit/enums";
import { Fields } from "../../kit/fields";
import { IdText, TimeText, useTimeText } from "../../kit/format";
import { stagger } from "../../kit/motion";
import { Card } from "../../kit/Page";
import { AuditTable, DepositsTable, TradesTable, useAudit, useDeposits, useTrades } from "../records/tables";
import { useWithdrawals, WithdrawalsTable } from "../wallet/withdrawalTable";
import { UserActions } from "./actions";
import { useRisk, useSecurity } from "./data";
import { BalancesTab, OrdersTab, PositionsTab } from "./money";
import { Notes, TagChips, TagsEditor } from "./NotesTags";
import { Contacts, ProfileTab } from "./profile";
import { RiskTab, Score } from "./risk";
import { SecurityTab } from "./security";

type UserSummary = AdminSchemas["UserSummary"];

/** The permissions that move an account's money, orders or positions. */
const MONEY: readonly string[] = ["ledger.adjust.request", "ledger.hold", "orders.cancel", "derivatives.write"];

/** withoutMoney is the administrator as they act on a test account cleared out (L4): all but what moves its money. */
function withoutMoney(admin: Admin): Admin {
  return { ...admin, permissions: admin.permissions.filter((p) => !MONEY.includes(p)) };
}

/**
 * UserPage is a user's page (design 2026-10-02 §4.1): the account at the
 * side with what the role may do to it, the records in tabs (?tab=) on the
 * right. /users/<id>.
 */
export default function UserPage({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const detail = useQuery({
    queryKey: ["admin", "user", id, "detail"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/users/{id}", { params: { path: { id } } })),
    retry: false,
  });
  const tabs = [
    "profile", "security", "balances", "orders", "positions", "trades",
    ...(can(admin, "withdrawals.read") ? ["deposits", "withdrawals"] : []),
    "risk", "notes",
    ...(can(admin, "audit.read") ? ["audit"] : []),
  ];
  const tab = tabs.includes(params.get("tab") ?? "") ? params.get("tab")! : "profile";
  const setTab = (v: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  if (detail.isError) return <ErrorState message={errorText(detail.error)} onRetry={() => void detail.refetch()} />;
  const u = detail.data;
  // A test account cleared out (L4) is closed and empty for good: its money,
  // orders and positions are left alone (L1); notes and tags stay. Nothing
  // moves money before the account is known, so a cleared-out one's tabs
  // never offer it while its page loads (A127).
  const acting = !u || u.purged_at ? withoutMoney(admin) : admin;
  return (
    <div className="flex flex-col gap-4">
      <Link to="/users" className="inline-flex w-fit items-center gap-1 text-sm text-fg-3 hover:text-fg-1">
        <ChevronLeft size={16} />
        {t("admin.user.back")}
      </Link>
      <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
        <aside className="flex flex-col gap-4">
          <Card className="stagger">{u ? <Summary admin={admin} user={u} /> : <Skeleton className="h-48 w-full" />}</Card>
          {u && (can(acting, "users.status") || can(acting, "orders.cancel")) && (
            <Card title={t("admin.user.actions")} className="stagger" style={stagger(1)}>
              <UserActions admin={acting} user={u} />
            </Card>
          )}
        </aside>
        <section className="card stagger min-w-0 p-4" style={stagger(2)}>
          <Tabs items={tabs.map((k) => ({ value: k, label: t(`admin.user.tabs.${k}`) }))} value={tab} onValueChange={setTab} />
          <div key={tab} className="mt-4 animate-rise">
            {tab === "profile" && (u ? <ProfileTab admin={admin} user={u} /> : <Skeleton className="h-32 w-full" />)}
            {tab === "security" && <SecurityTab admin={admin} userId={id} />}
            {tab === "balances" && <BalancesTab admin={acting} userId={id} />}
            {tab === "orders" && <OrdersTab admin={acting} userId={id} />}
            {tab === "positions" && <PositionsTab admin={acting} userId={id} />}
            {tab === "trades" && <UserTrades userId={id} />}
            {tab === "deposits" && <UserDeposits userId={id} />}
            {tab === "withdrawals" && <UserWithdrawals userId={id} />}
            {tab === "risk" && (u ? <RiskTab admin={admin} userId={id} status={u.status} /> : <Skeleton className="h-32 w-full" />)}
            {tab === "notes" && (
              <div className="flex flex-col gap-6">
                <div>
                  <h3 className="mb-2 text-sm font-semibold">{t("admin.user.tags")}</h3>
                  <TagsEditor admin={admin} userId={id} tags={u?.tags ?? []} />
                </div>
                <div>
                  <h3 className="mb-2 text-sm font-semibold">{t("admin.user.notes")}</h3>
                  <Notes admin={admin} userId={id} />
                </div>
              </div>
            )}
            {tab === "audit" && <UserAudit userId={id} />}
          </div>
        </section>
      </div>
    </div>
  );
}

/** Summary is the account at a glance: who, its contacts (masked until revealed), status, tags, last sign-in and risk. */
function Summary({ admin, user }: { admin: Admin; user: UserSummary }) {
  const { t } = useTranslation();
  const timeText = useTimeText();
  const sec = useSecurity(user.id);
  const risk = useRisk(user.id);
  const latest = risk.data?.[0];
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Avatar name={user.username || user.id.replace(/-/g, "").slice(-2)} src={user.avatar_url ?? undefined} seed={user.id} size={48} />
        <div className="min-w-0">
          <div className="truncate font-mono text-sm font-medium text-fg-1" data-testid="user-username">
            {user.username || "—"}
          </div>
          <div className="text-xs text-fg-3">{t("admin.user.uid")}</div>
          <IdText value={user.id} chars={13} className="text-sm" />
        </div>
      </div>
      <Contacts admin={admin} userId={user.id} />
      <div className="flex flex-wrap items-center gap-2">
        <EnumBadge group="userStatus" code={user.status} />
        {/* Its kind (L1): a person, a bot, a test account or HOUSE's. */}
        <span data-testid="user-kind" data-kind={user.kind}>
          <EnumBadge group="userKind" code={user.kind} />
        </span>
        {/* A test account cleared out (L4): when, and why its money is left alone. */}
        {user.purged_at && (
          <span data-testid="user-purged" title={t("admin.user.purgedHint")}>
            <Badge tone="neutral">{t("admin.user.purged", { time: timeText(user.purged_at) })}</Badge>
          </span>
        )}
        <TagChips tags={user.tags} />
      </div>
      <Fields
        label={t("admin.user.tabs.profile")}
        narrow
        items={[
          { label: t("admin.user.registered"), value: <TimeText value={user.created_at} /> },
          { label: t("admin.user.lastLogin"), value: sec.data ? <TimeText value={sec.data.last_login_at} /> : "—" },
          {
            label: t("admin.user.riskScore"),
            hint: t("admin.user.riskScoreHint"),
            value: latest ? (
              <span className="inline-flex items-center gap-1.5">
                <Score value={latest.score} />
                <EnumBadge group="riskAction" code={latest.action} />
              </span>
            ) : risk.isPending ? (
              "…"
            ) : (
              <span className="text-fg-3">{t("admin.user.noRisk")}</span>
            ),
          },
          { label: t("admin.user.profileRows.region"), value: user.region || "—" },
          { label: t("admin.user.profileRows.kyc"), value: user.kyc_level },
        ]}
      />
    </div>
  );
}

function UserTrades({ userId }: { userId: string }) {
  return <TradesTable list={useTrades({ user_id: userId })} />;
}

function UserDeposits({ userId }: { userId: string }) {
  return <DepositsTable list={useDeposits({ user_id: userId })} withUser={false} />;
}

function UserWithdrawals({ userId }: { userId: string }) {
  return <WithdrawalsTable list={useWithdrawals({ user_id: userId, status: "ALL" })} withUser={false} />;
}

function UserAudit({ userId }: { userId: string }) {
  return <AuditTable list={useAudit({ target: `user:${userId}` })} />;
}
