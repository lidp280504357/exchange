import { formatDecimal } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { ErrorState, KeyTag, ShortList, Skeleton, SummaryRow, SummaryTable, withKeys, type SummaryStatus } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { CircleCheck, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { TimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { Amount, AmountGrid, few, FlagState, Lines, lowest, OnOff, positive, type Translate } from "../../kit/summary";

// The launch checklist (design 2026-10-04 §4.6, D2): what of the
// learning setup is still on, read-only. Each item's state now (from the
// services, read again every 30 seconds) in a sentence, with what a launch
// needs and the raw values once its row is opened (A94), and where it is
// changed; "可上线" once every item is OK. The deployment side (domains,
// keys, third-party accounts) is the launch handbook's.

type Item = AdminSchemas["LaunchItem"];
type Key = Item["key"];
type Status = Item["status"];

const tone: Record<Status, SummaryStatus["tone"]> = { OK: "success", FAIL: "danger", PENDING: "warn", UNKNOWN: "warn" };

/** Where each item is changed: a page of the console, or the launch handbook. */
const FIX: Record<Key, string | null> = {
  welcome_credits: "/platform#welcome", test_mode: "/platform", registration: "/platform", admin_totp: "/settings", two_person: "/risk",
  test_assets: "/risk", custodian: null, withdraw: "/risk", brand: "/platform", coin_profile: "/sim/token", legal: "/pages",
  third_party: null, admins: "/admins", domain: "/platform", house: "/house", margin: "/risk", insurance: "/derivatives",
  coin_m: "/risk", app_downloads: "/platform/apps", products: "/instruments",
};

export const launchKey = ["admin", "launch-checklist"];

export default function Launch() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: launchKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/launch-checklist")),
    refetchInterval: 30_000,
  });
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
            {!c.ready && <span className="text-sm text-fg-2">{open.map((it) => t(`admin.launch.items.${it.key}.name`)).join(t("admin.summary.sep"))}</span>}
            <span className="text-xs text-fg-3">
              {t("admin.launch.checkedAt")} <TimeText value={c.checked_at} style="timeSeconds" />
              {t("admin.summary.clause")}
              {t("admin.launch.scope")}
            </span>
          </div>
        </div>
      ) : (
        <Skeleton className="h-20 w-full" />
      )}
      <Card>
        {!c ? (
          <Skeleton className="h-96 w-full" />
        ) : (
          <SummaryTable label={t("admin.nav.launch")} headings={{ item: t("admin.launch.item"), status: t("admin.common.status"), summary: t("admin.launch.current") }}>
            {c.items.map((it) => {
              const d = describe(it, t);
              return (
                <SummaryRow
                  key={it.key}
                  data-testid={`launch-row-${it.key}`}
                  statusTestId={`launch-${it.key}`}
                  statusData={it.status}
                  title={t(`admin.launch.items.${it.key}.name`)}
                  source={withKeys(t(`admin.launch.items.${it.key}.source`))}
                  status={{ tone: tone[it.status], label: t(`admin.launch.status.${it.status}`) }}
                  summary={d.summary}
                  details={
                    <div className="flex flex-col gap-3">
                      <Lines items={[[t("admin.launch.required"), t(`admin.launch.items.${it.key}.required`)], ...(d.lines ?? [])]} />
                      {d.extra}
                    </div>
                  }
                  action={
                    FIX[it.key] ? (
                      <Link to={FIX[it.key]!} className="text-info-strong hover:underline">
                        {t("admin.launch.fix")}
                      </Link>
                    ) : (
                      <span className="text-xs text-fg-3">{t("admin.launch.handbook")}</span>
                    )
                  }
                />
              );
            })}
          </SummaryTable>
        )}
      </Card>
    </Page>
  );
}

type Line = [ReactNode, ReactNode];

/** An item in words: a sentence of its state now, its raw values as labelled lines, and anything longer (a table of amounts). */
type Described = { summary: ReactNode; lines?: Line[]; extra?: ReactNode };

/** flagLines are a switch's raw values: its key and, when it has them, its rules as stored. */
function flagLines(flag: unknown, rules: unknown, t: Translate): Line[] {
  const lines: Line[] = [[t("admin.summary.flag"), <KeyTag key="k">{String(flag)}</KeyTag>]];
  if (rules) lines.push([t("admin.summary.rules"), <code key="r" className="break-all font-mono text-xs text-fg-2">{JSON.stringify(rules)}</code>]);
  return lines;
}

/** describe says an item's state now in a sentence, with its raw values for the opened row. */
function describe({ key, value: v, status }: Item, t: Translate): Described {
  if (status === "PENDING") return { summary: <span className="text-fg-3">{t("admin.launch.pending")}</span> };
  // An unknown third party still shows what the other services reported.
  if (status === "UNKNOWN" && key !== "third_party") return { summary: <span className="text-warn-strong">{t("admin.launch.unknown")}</span> };
  const set = (b: unknown) => t(b === true ? "admin.summary.set" : b === false ? "admin.summary.unset" : "admin.summary.unknown");
  const sep = t("admin.summary.sep");
  switch (key) {
    case "welcome_credits": {
      const credits = (v.credits as { asset: string; amount: string }[] | undefined) ?? [];
      const given = credits.filter((c) => positive(c.amount));
      return {
        summary: (
          <span>
            {given.length ? (
              <>
                {t("admin.summary.launch.welcomeSome")} <ShortList items={given.map((c) => <Amount key={c.asset} value={c.amount} asset={c.asset} />)} />
              </>
            ) : (
              t("admin.launch.nothing")
            )}
            <span className="text-fg-3">
              {t("admin.summary.clause")}
              {t(v.switch ? "admin.summary.launch.masterOn" : "admin.summary.launch.masterOff")}
            </span>
          </span>
        ),
        lines: [
          [t("admin.summary.welcome.master"), <FlagState key="m" flag="ledger.welcome_credit" on={v.switch} scope={false} />],
          ...credits.map((c): Line => [c.asset, <Amount key={c.asset} value={c.amount} asset={c.asset} />]),
        ],
      };
    }
    case "test_mode":
      return {
        summary: v.enabled ? t(v.banner ? "admin.summary.launch.testOnBanner" : "admin.summary.launch.testOn") : t("admin.summary.launch.testOff"),
        lines: [
          [t("admin.summary.launch.testMode"), <OnOff key="m" on={v.enabled} />],
          [t("admin.summary.launch.banner"), <OnOff key="b" on={v.banner} />],
        ],
      };
    case "registration":
      return { summary: t(`admin.summary.launch.registration.${String(v.status)}`) };
    case "admin_totp":
      // The console's setting admin.require_totp (N1), with the active ADMINs whose authenticator is bound.
      return {
        summary: <OnOff on={v.enabled} />,
        lines: [
          [t("admin.summary.launch.setting"), <KeyTag key="k">{String(v.setting ?? "admin.require_totp")}</KeyTag>],
          [t("admin.access.boundAdmins"), String(v.bound_admins ?? "—")],
        ],
      };
    case "two_person":
    case "test_assets":
    case "withdraw":
    case "coin_m":
      // On for everyone without rules is the test server's (coin_m).
      return { summary: <FlagState on={v.enabled} rules={v.rules} />, lines: flagLines(v.flag, v.rules, t) };
    case "custodian": {
      const host = typeof v.gateway_host === "string" ? v.gateway_host : "";
      return {
        summary: host ? (
          <span className="inline-flex flex-wrap items-center gap-1.5">
            {t("admin.summary.launch.gateway")} <KeyTag>{host}</KeyTag>
            {host.includes("mock") && <span className="text-fg-3">{t("admin.summary.launch.mock")}</span>}
          </span>
        ) : (
          t("admin.launch.notConfigured")
        ),
      };
    }
    case "brand":
      return {
        summary: (
          <span>
            {t("admin.summary.launch.brand", { name: String(v.name ?? ""), logo: set(v.logo), favicon: set(v.favicon) })}
            {v.default_name === true && (
              <>
                {t("admin.summary.clause")}
                <span className="text-danger-strong">{t("admin.launch.defaultName")}</span>
              </>
            )}
          </span>
        ),
      };
    case "coin_profile":
      return { summary: t("admin.summary.launch.coin", { asset: String(v.asset ?? ""), name: String(v.display_name ?? ""), logo: set(v.logo) }) };
    case "legal": {
      const missing = (v.missing as string[] | undefined) ?? [];
      return { summary: missing.length ? t("admin.launch.legalMissing", { slugs: missing.join(sep) }) : t("admin.launch.legalAll") };
    }
    case "third_party": {
      const parts = [
        ["turnstile", v.turnstile],
        ["mail", v.mail],
        ["alchemy", v.alchemy],
      ] as const;
      const name = (k: string) => t(`admin.summary.launch.third.${k}`);
      const missing = parts.filter(([, b]) => b !== true).map(([k]) => name(k));
      return {
        summary: missing.length
          ? t("admin.summary.launch.thirdSome", { ready: parts.filter(([, b]) => b === true).map(([k]) => name(k)).join(sep) || "—", missing: missing.join(sep) })
          : t("admin.summary.launch.thirdAll"),
        lines: parts.map(([k, b]): Line => [name(k), set(b)]),
      };
    }
    case "admins": {
      const without = (v.without_totp as string[] | undefined) ?? [];
      const n = Number(v.active_admins ?? 0);
      return {
        summary: without.length ? t("admin.summary.launch.adminsSome", { n, m: without.length }) : t("admin.summary.launch.adminsAll", { n }),
        lines: without.length ? [[t("admin.summary.launch.withoutTotp"), <span key="w">{without.join(sep)}</span>]] : undefined,
      };
    }
    case "domain":
      return {
        summary: v.domain
          ? t("admin.launch.domainNow", { domain: String(v.domain), host: String(v.console_host ?? "") })
          : t("admin.summary.launch.domainNone", { host: String(v.console_host ?? "") }),
      };
    case "house": {
      const backed = Object.entries((v.backed as Record<string, string> | undefined) ?? {}).sort(([a], [b]) => a.localeCompare(b));
      const zero = backed.filter(([, b]) => !positive(b)).map(([a]) => a);
      const min = lowest(backed);
      return {
        summary: (
          <span className="inline-flex flex-wrap items-center gap-x-1.5">
            <OnOff on={v.enabled} />
            <span>
              {!backed.length
                ? t("admin.launch.noBacked")
                : zero.length
                  ? t("admin.summary.launch.backedZero", { n: backed.length, k: zero.length, assets: few(zero, t) })
                  : t("admin.summary.launch.backedAll", { n: backed.length, asset: min?.[0] ?? "", min: formatMin(min) })}
            </span>
          </span>
        ),
        lines: flagLines(v.flag, undefined, t),
        extra: backed.length ? <AmountGrid rows={backed} problem={(_, b) => !positive(b)} /> : undefined,
      };
    }
    case "margin":
      // The switches as a launch has them (design 2026-10-06 §8): on for everyone without rules is the test server's.
      return {
        summary: (
          <span className="inline-flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="inline-flex items-center gap-1.5">
              {t("admin.summary.launch.margin")} <FlagState on={v.enabled} rules={!v.global} />
            </span>
            {v.enabled === true && (
              <>
                <span className="inline-flex items-center gap-1.5">
                  {t("admin.summary.launch.liquidation")} <OnOff on={v.liquidation} />
                </span>
                <span className="inline-flex items-center gap-1.5">
                  {t("admin.summary.launch.autoBorrow")} <FlagState on={v.auto_borrow} rules={!v.auto_borrow_global} />
                </span>
              </>
            )}
          </span>
        ),
        lines: [
          [t("admin.summary.launch.margin"), <FlagState key="e" flag="margin.enabled" on={v.enabled} rules={!v.global} />],
          [t("admin.summary.launch.liquidation"), <FlagState key="l" flag="margin.liquidation" on={v.liquidation} scope={false} />],
          [t("admin.summary.launch.autoBorrow"), <FlagState key="a" flag="margin.auto_borrow" on={v.auto_borrow} rules={!v.auto_borrow_global} />],
          ...(v.rules ? [[t("admin.summary.rules"), <code key="r" className="break-all font-mono text-xs text-fg-2">{JSON.stringify(v.rules)}</code>] as Line] : []),
        ],
      };
    case "insurance": {
      // The contracts open by type (§3.5), then each settlement asset of one, its fund now (design 2026-10-06 §2.7).
      const balances = Object.entries((v.balances as Record<string, string> | undefined) ?? {}).sort(([a], [b]) => a.localeCompare(b));
      const short = (v.short as string[] | undefined) ?? [];
      const opened = (v.open as { USDT?: number; COIN?: number } | undefined) ?? {};
      const min = lowest(balances);
      return {
        summary: !balances.length ? (
          t("admin.launch.noOpenContracts")
        ) : (
          <span>
            {t("admin.summary.launch.contractsOpen", { usdt: opened.USDT ?? 0, coin: opened.COIN ?? 0 })}
            {t("admin.summary.clause")}
            {short.length
              ? t("admin.summary.launch.fundsShort", { n: balances.length, k: short.length, assets: few(short, t) })
              : t("admin.summary.launch.fundsAll", { n: balances.length, asset: min?.[0] ?? "", min: formatMin(min) })}
          </span>
        ),
        extra: balances.length ? <AmountGrid rows={balances} problem={(asset) => short.includes(asset)} /> : undefined,
      };
    }
    case "products": {
      // Which product lines are open (K3), for information.
      const lines = ["spot", "usdt_m", "coin_m"] as const;
      const closed = lines.filter((l) => (v[l] as { enabled?: boolean } | undefined)?.enabled === false);
      return {
        summary: closed.length
          ? t("admin.summary.launch.productsClosed", { lines: closed.map((l) => t(`admin.products.lines.${l}`)).join(sep) })
          : t("admin.summary.launch.productsOpen"),
        lines: lines.map((l): Line => {
          const st = (v[l] as { enabled?: boolean; closed_at?: string | null } | undefined) ?? {};
          return [
            t(`admin.products.lines.${l}`),
            <span key={l} className="inline-flex items-center gap-1.5">
              <FlagState flag={`product.${l}`} on={st.enabled !== false} scope={false} />
              {st.closed_at && (
                <span className="text-xs text-fg-3">
                  <TimeText value={st.closed_at} />
                </span>
              )}
            </span>,
          ];
        }),
      };
    }
    case "app_downloads": {
      const offered = (p: "android" | "ios") => {
        const m = v[p] as string | null | undefined;
        return m ? t(`admin.apps.modes.${m}`) : t("admin.launch.appNone");
      };
      return {
        summary: t("admin.summary.launch.apps", {
          android: offered("android"),
          ios: offered("ios"),
          entry: typeof v.entry_visible === "boolean" ? t(v.entry_visible ? "admin.apps.entry.on" : "admin.apps.entry.off") : "—",
        }),
      };
    }
  }
  return { summary: "—" };
}

/** formatMin is the lowest amount grouped by thousands (99,999.5). */
function formatMin(min: [string, string] | undefined): string {
  return min ? formatDecimal(min[1], {}) : "—";
}
