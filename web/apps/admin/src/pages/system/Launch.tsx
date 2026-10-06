import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, Skeleton, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { CircleCheck, TriangleAlert } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Num, TimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";

// The launch checklist (design 2026-10-04 §4.6, D2): what of the
// learning setup is still on, read-only. Each item's state now (from the
// services, read again every 30 seconds), what a launch needs and where it
// is changed; "可上线" once every item is OK. The deployment side (domains,
// keys, third-party accounts) is the launch handbook's.

type Item = AdminSchemas["LaunchItem"];
type Key = Item["key"];
type Status = Item["status"];

const tone: Record<Status, "success" | "danger" | "neutral" | "warn"> = { OK: "success", FAIL: "danger", PENDING: "neutral", UNKNOWN: "warn" };

/** Where each item is changed: a page of the console, or the launch handbook. */
const FIX: Record<Key, string | null> = {
  welcome_credits: "/platform#welcome", test_mode: "/platform", registration: "/platform", admin_totp: "/risk", two_person: "/risk",
  test_assets: "/risk", custodian: null, withdraw: "/risk", brand: "/platform", coin_profile: "/sim/token", legal: "/pages",
  third_party: null, admins: "/admins", domain: "/platform", house: "/house", margin: "/risk", insurance: "/derivatives",
  coin_m: "/risk",
};

export const launchKey = ["admin", "launch-checklist"];

export default function Launch() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: launchKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/launch-checklist")),
    refetchInterval: 30_000,
  });
  const columns = useMemo<ColumnDef<Item, unknown>[]>(
    () => [
      {
        id: "item", header: t("admin.launch.item"),
        cell: ({ row: { original: it } }) => (
          <span className="flex flex-col">
            <span className="font-medium text-fg-1">{t(`admin.launch.items.${it.key}.name`)}</span>
            <span className="text-xs text-fg-3">{t(`admin.launch.items.${it.key}.source`)}</span>
          </span>
        ),
      },
      { id: "current", header: t("admin.launch.current"), cell: ({ row }) => <Current item={row.original} /> },
      { id: "required", header: t("admin.launch.required"), cell: ({ row }) => <span className="text-sm text-fg-2">{t(`admin.launch.items.${row.original.key}.required`)}</span> },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row: { original: it } }) => (
          <span data-testid={`launch-${it.key}`} data-status={it.status}>
            <Badge tone={tone[it.status]} dot>
              {t(`admin.launch.status.${it.status}`)}
            </Badge>
          </span>
        ),
      },
      {
        id: "fix", header: "",
        cell: ({ row: { original: it } }) =>
          FIX[it.key] ? (
            <Link to={FIX[it.key]!} className="text-sm text-info-strong hover:underline">
              {t("admin.launch.fix")}
            </Link>
          ) : (
            <span className="text-xs text-fg-3">{t("admin.launch.handbook")}</span>
          ),
      },
    ],
    [t],
  );
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  const c = q.data;
  const open = (c?.items ?? []).filter((it) => it.status !== "OK");
  return (
    <Page title={t("admin.nav.launch")} help={t("admin.launch.help")}>
      {c ? (
        <div
          className={
            c.ready
              ? "flex items-center gap-3 rounded-2 border border-success bg-bg-1 px-4 py-3"
              : "flex items-start gap-3 rounded-2 border border-warn bg-bg-1 px-4 py-3"
          }
          data-testid="launch-verdict"
        >
          {c.ready ? <CircleCheck className="text-success-strong" size={22} /> : <TriangleAlert className="mt-0.5 text-warn-strong" size={22} />}
          <div className="flex flex-col gap-1">
            <span className="text-md font-semibold text-fg-1">{c.ready ? t("admin.launch.ready") : t("admin.launch.notReady", { n: open.length })}</span>
            {!c.ready && <span className="text-sm text-fg-2">{open.map((it) => t(`admin.launch.items.${it.key}.name`)).join("、")}</span>}
            <span className="text-xs text-fg-3">
              {t("admin.launch.checkedAt")} <TimeText value={c.checked_at} style="timeSeconds" /> · {t("admin.launch.scope")}
            </span>
          </div>
        </div>
      ) : (
        <Skeleton className="h-20 w-full" />
      )}
      <Card>
        <DataTable
          columns={columns}
          data={c?.items ?? []}
          getRowId={(it) => it.key}
          loading={q.isPending}
          error={q.error}
          onRetry={() => void q.refetch()}
          density="compact"
          aria-label={t("admin.nav.launch")}
        />
      </Card>
    </Page>
  );
}

/** Current says an item's state now, as its source reports it. */
function Current({ item: { key, value: v, status } }: { item: Item }) {
  const { t } = useTranslation();
  if (status === "PENDING") return <span className="text-sm text-fg-3">{t("admin.launch.pending")}</span>;
  // An unknown third party still shows what the other services reported.
  if (status === "UNKNOWN" && key !== "third_party") return <span className="text-sm text-warn-strong">{t("admin.launch.unknown")}</span>;
  const on = (b: unknown) => t(b ? "admin.launch.on" : "admin.launch.off");
  const yes = (b: unknown) => (b === true ? "✓" : b === false ? "✗" : "?");
  let body: ReactNode;
  switch (key) {
    case "welcome_credits": {
      const credits = (v.credits as { asset: string; amount: string }[] | undefined) ?? [];
      body = (
        <>
          {credits.length ? credits.map((c) => `${c.amount} ${c.asset}`).join(" · ") : t("admin.launch.nothing")}
          <span className="text-fg-3"> · {t("admin.launch.master", { on: on(v.switch) })}</span>
        </>
      );
      break;
    }
    case "test_mode":
      body = (
        <>
          {on(v.enabled)}
          {v.enabled ? <span className="text-fg-3"> · {t("admin.launch.banner", { on: on(v.banner) })}</span> : null}
        </>
      );
      break;
    case "registration":
      body = t(`admin.launch.registration.${String(v.status)}`);
      break;
    case "admin_totp":
    case "two_person":
    case "test_assets":
    case "withdraw":
      body = (
        <>
          <span className="font-mono text-xs text-fg-3">{String(v.flag)}</span> {on(v.enabled)}
          {v.rules ? <span className="text-fg-3"> · {t("admin.launch.rules")}</span> : null}
        </>
      );
      break;
    case "custodian":
      body = v.gateway_host ? <span className="font-mono">{String(v.gateway_host)}</span> : t("admin.launch.notConfigured");
      break;
    case "brand":
      body = (
        <>
          {t("admin.launch.brandNow", { name: String(v.name ?? ""), logo: yes(v.logo), favicon: yes(v.favicon) })}
          {v.default_name === true && <span className="text-danger-strong"> · {t("admin.launch.defaultName")}</span>}
        </>
      );
      break;
    case "coin_profile":
      body = t("admin.launch.coinNow", { asset: String(v.asset ?? ""), name: String(v.display_name ?? ""), logo: yes(v.logo) });
      break;
    case "legal": {
      const missing = (v.missing as string[] | undefined) ?? [];
      body = missing.length ? t("admin.launch.legalMissing", { slugs: missing.join(", ") }) : t("admin.launch.legalAll");
      break;
    }
    case "third_party":
      body = t("admin.launch.thirdNow", { turnstile: yes(v.turnstile), mail: yes(v.mail), alchemy: yes(v.alchemy) });
      break;
    case "admins": {
      const without = (v.without_totp as string[] | undefined) ?? [];
      body = (
        <>
          {t("admin.launch.adminsNow", { n: Number(v.active_admins ?? 0) })}
          {without.length > 0 && <span className="text-danger-strong"> · {t("admin.launch.withoutTotp", { emails: without.join(", ") })}</span>}
        </>
      );
      break;
    }
    case "domain":
      body = t("admin.launch.domainNow", { domain: String(v.domain || "—"), host: String(v.console_host ?? "") });
      break;
    case "margin":
      // The switches as a launch has them (design 2026-10-06 §8): on for everyone without rules is the test server's.
      body = (
        <>
          <span className="font-mono text-xs text-fg-3">margin.enabled</span>{" "}
          <span className={v.global ? "text-danger-strong" : undefined}>
            {on(v.enabled)}
            {v.global ? ` · ${t("admin.launch.marginGlobal")}` : ""}
          </span>
          {v.enabled === true && (
            <>
              <span className="text-fg-3"> · </span>
              <span className="font-mono text-xs text-fg-3">margin.liquidation</span>{" "}
              <span className={v.liquidation ? undefined : "text-danger-strong"}>{on(v.liquidation)}</span>
              <span className="text-fg-3"> · </span>
              <span className="font-mono text-xs text-fg-3">margin.auto_borrow</span>{" "}
              <span className={v.auto_borrow_global ? "text-danger-strong" : undefined}>
                {on(v.auto_borrow)}
                {v.auto_borrow_global ? ` · ${t("admin.launch.marginGlobal")}` : ""}
              </span>
            </>
          )}
        </>
      );
      break;
    case "insurance": {
      // The contracts open by type (§3.5), then each settlement asset of one, its fund now (design 2026-10-06 §2.7).
      const balances = Object.entries((v.balances as Record<string, string> | undefined) ?? {}).sort(([a], [b]) => a.localeCompare(b));
      const short = (v.short as string[] | undefined) ?? [];
      const open = (v.open as { USDT?: number; COIN?: number } | undefined) ?? {};
      body = (
        <>
          <span className="text-fg-3">{t("admin.launch.contractsOpen", { usdt: open.USDT ?? 0, coin: open.COIN ?? 0 })} · </span>
          {balances.length
            ? balances.map(([asset, balance], i) => (
                <span key={asset} className={short.includes(asset) ? "text-danger-strong" : undefined}>
                  {i > 0 && " · "}
                  <Num value={balance} unit={asset} />
                </span>
              ))
            : t("admin.launch.noOpenContracts")}
          {short.length > 0 && <span className="text-danger-strong"> · {t("admin.launch.insuranceShort", { assets: short.join(", ") })}</span>}
        </>
      );
      break;
    }
    case "coin_m": {
      // On for everyone without rules is the test server's.
      const global = v.enabled === true && !v.rules;
      body = (
        <>
          <span className="font-mono text-xs text-fg-3">{String(v.flag)}</span>{" "}
          <span className={global ? "text-danger-strong" : undefined}>
            {on(v.enabled)}
            {global ? ` · ${t("admin.launch.coinMGlobal")}` : ""}
          </span>
          {v.rules ? <span className="text-fg-3"> · {t("admin.launch.rules")}</span> : null}
        </>
      );
      break;
    }
    case "house": {
      const backed = Object.entries((v.backed as Record<string, string> | undefined) ?? {});
      body = (
        <>
          <span className="font-mono text-xs text-fg-3">{String(v.flag)}</span> {on(v.enabled)}
          <span className="text-fg-3"> · </span>
          {backed.length
            ? backed.map(([asset, balance], i) => (
                <span key={asset} className={Number(balance) > 0 ? undefined : "text-danger-strong"}>
                  {i > 0 && " · "}
                  <Num value={balance} unit={asset} />
                </span>
              ))
            : t("admin.launch.noBacked")}
        </>
      );
      break;
    }
  }
  return <span className="text-sm text-fg-1">{body}</span>;
}
