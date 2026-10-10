import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, Skeleton, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Num, TimeText, UserCell } from "../../kit/format";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { AssetProfileSection } from "../instruments/profile";
import { simKey, useSim, type SimBot } from "./common";

// The platform coin (ASTRA design §5.2, §6.1): its profile as the sites
// show it, and who holds it — the bots, the users, the platform's
// accounts — as the ledger holds it now (A123: ledger-service's balances,
// a margin debt counting against its holder).

type SimToken = AdminSchemas["SimToken"];
type Holder = SimToken["top"][number];

const right: DataColumnMeta = { align: "right" };

/** share is a part of the whole as a percentage; a sliver shows as under 0.01%, nearly all as over 99.99%. */
function share(part: string, whole: string) {
  if (!dec.isDecimal(part) || !dec.gt(whole, "0")) return "—";
  const p = (dec.toNumber(part) / dec.toNumber(whole)) * 100;
  if (p > 0 && p < 0.01) return "<0.01%";
  if (p > 99.99 && p < 100) return ">99.99%";
  return `${p.toFixed(2)}%`;
}

export default function SimTokenPage({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const sim = useSim(15_000);
  const q = useQuery({
    queryKey: [...simKey, "token"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/token")),
    refetchInterval: 60_000,
  });
  const bots = useMemo(() => new Map((sim.data?.bots ?? []).map((b) => [b.user_id, b])), [sim.data]);
  const tok = q.data;
  const platform = tok ? tok.platform.reduce((a, p) => dec.add(a, p.amount), "0") : "0";
  // The test accounts apart when told (A125); HOUSE is in the platform's rows.
  const held = tok ? dec.add(dec.add(dec.add(tok.bots.amount, tok.users.amount), tok.test?.amount ?? "0"), platform) : "0";
  const columns = useMemo<ColumnDef<Holder, unknown>[]>(
    () => [
      { id: "rank", header: "#", cell: ({ row }) => <span className="font-mono text-xs text-fg-3">{row.index + 1}</span> },
      { id: "account", header: t("admin.sim.account"), cell: ({ row }) => <Account id={row.original.user_id} bot={bots.get(row.original.user_id)} /> },
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={tok?.asset} /> },
      { id: "share", header: t("admin.sim.shareOf"), meta: right, cell: ({ row }) => <span className="font-mono text-xs">{share(row.original.amount, held)}</span> },
    ],
    [t, bots, tok?.asset, held],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const value = tok?.price ? dec.mul(tok.issued, tok.price) : null;
  return (
    <Page title={t("admin.nav.simToken")} help={t("admin.sim.tokenHelp")}>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Card title={t("admin.sim.issued")} className="stagger">
          {tok ? (
            <div className="flex flex-col gap-1">
              <Num value={tok.issued} decimals={0} unit={tok.asset} className="text-lg" />
              <span className="text-xs text-fg-3">
                {t("admin.sim.asOf")} <TimeText value={tok.at} style="timeSeconds" />
              </span>
            </div>
          ) : <Skeleton className="h-14 w-full" />}
        </Card>
        <Card title={t("admin.sim.marketValue")} className="stagger" style={stagger(1)}>
          {tok ? (
            <div className="flex flex-col gap-1">
              {value ? <Num value={value} decimals={0} unit="USDT" className="text-lg" /> : <span className="text-lg text-fg-3">—</span>}
              <span className="text-xs text-fg-3">
                {t("admin.sim.last")} <Num value={tok.price} />
              </span>
            </div>
          ) : <Skeleton className="h-14 w-full" />}
        </Card>
        <Holding title={t("admin.sim.botsHold")} h={tok?.bots} asset={tok?.asset} held={held} index={2} testid="sim-token-bots" />
        <Holding title={t("admin.sim.usersHold")} h={tok?.users} asset={tok?.asset} held={held} index={3} testid="sim-token-users" />
      </div>
      <Card title={t("admin.sim.distribution")} className="stagger" style={stagger(4)}>
        {tok ? <Distribution tok={tok} platform={platform} held={held} /> : <Skeleton className="h-12 w-full" />}
      </Card>
      <div className="grid gap-3 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
        <Card title={t("admin.sim.topHolders")} className="stagger" style={stagger(5)}>
          <DataTable
            columns={columns}
            data={tok?.top ?? []}
            getRowId={(h) => h.user_id}
            loading={q.isPending}
            empty={t("admin.sim.noHolders")}
            density="compact"
            aria-label={t("admin.sim.topHolders")}
          />
        </Card>
        <Card title={t("admin.sim.profile")} className="stagger" style={stagger(6)}>
          {tok ? <AssetProfileSection code={tok.asset} writable={can(admin, "instruments.write")} /> : <Skeleton className="h-40 w-full" />}
        </Card>
      </div>
    </Page>
  );
}

function Holding({ title, h, asset, held, index, testid }: {
  title: string; h?: SimToken["bots"]; asset?: string; held: string; index: number; testid: string;
}) {
  const { t } = useTranslation();
  return (
    <Card title={title} className="stagger" style={stagger(index)}>
      {h ? (
        <div className="flex flex-col gap-1" data-testid={testid}>
          <Num value={h.amount} decimals={2} unit={asset} className="text-lg" />
          <span className="text-xs text-fg-3">
            {share(h.amount, held)} · {t("admin.sim.holders", { n: h.holders })}
          </span>
        </div>
      ) : <Skeleton className="h-14 w-full" />}
    </Card>
  );
}

/** Distribution is the coin's holders as one bar: the bots, the users, the test accounts when told apart, the platform's accounts. */
function Distribution({ tok, platform, held }: { tok: SimToken; platform: string; held: string }) {
  const { t } = useTranslation();
  const parts = [
    { key: "bots", label: t("admin.sim.botsHold"), amount: tok.bots.amount, className: "bg-chart-1" },
    { key: "users", label: t("admin.sim.usersHold"), amount: tok.users.amount, className: "bg-chart-3" },
    ...(tok.test ? [{ key: "test", label: t("admin.sim.testHold"), amount: tok.test.amount, className: "bg-chart-4" }] : []),
    { key: "platform", label: t("admin.sim.platformHold"), amount: platform, className: "bg-chart-5" },
  ];
  const width = (v: string) => (dec.gt(held, "0") ? Math.max(0, (dec.toNumber(v) / dec.toNumber(held)) * 100) : 0);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-bg-2" role="img" aria-label={t("admin.sim.distribution")}>
        {parts.map((p) => (
          <span key={p.key} className={p.className} style={{ width: `${width(p.amount)}%` }} />
        ))}
      </div>
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm">
        {parts.map((p) => (
          <span key={p.key} className="inline-flex items-center gap-1.5">
            <span className={`size-2.5 rounded-full ${p.className}`} />
            {p.label} <Num value={p.amount} decimals={2} /> <span className="text-xs text-fg-3">{share(p.amount, held)}</span>
          </span>
        ))}
        {tok.platform.map((p) => (
          <span key={p.account_type} className="text-xs text-fg-3">
            {p.account_type} <Num value={p.amount} />
          </span>
        ))}
      </div>
      {!dec.eq(held, tok.issued) && <p className="text-xs text-warn-strong">{t("admin.sim.heldDiffers", { held, issued: tok.issued })}</p>}
      {tok.partial.map((p) => (
        <p key={p} className="text-xs text-fg-3" data-testid={`sim-token-partial-${p}`}>
          {t(`admin.sim.partial.${p}`)}
        </p>
      ))}
    </div>
  );
}

/** Account is a holder: a bot with its label and role, or a user. */
function Account({ id, bot }: { id: string; bot?: SimBot }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex items-center gap-1.5">
      <UserCell id={id} />
      {bot && (
        <Badge tone="info">
          {t("admin.sim.bot")} {bot.label} · {t(`admin.sim.roles.${bot.role}`)}
        </Badge>
      )}
    </span>
  );
}
