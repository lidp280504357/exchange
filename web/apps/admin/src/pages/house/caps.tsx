import { ApiError, dec, errorText, formatDecimal } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Input, KeyTag, Skeleton, SummaryRow, SummaryTable } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Num, TimeText } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { Card } from "../../kit/Page";
import { Lines } from "../../kit/summary";
import { direction, holdings, HOUSE_CAPS, inRange, LEVERAGE_CEILING, over, stepOK, stepRange, totalOver, type CapName, type Holding } from "./capsRules";

// HOUSE's caps at run time (user 2026-10-07, A69; market-maker review C45):
// what HOUSE quotes within - each cap with its unit, current and first
// value, allowed range, purpose and what lowering or raising it does (user
// 06:0x; a row each since A94, its purpose and value shown, the rest once
// opened), the per-asset and total caps beside what HOUSE holds now (review
// R18) - with the request that waits and the latest changes; a change is
// a HOUSE_CAPS request a second administrator approves, its dialog listing
// each cap from and to with what that does, in red where HOUSE would stop
// buying.

type View = AdminSchemas["HouseCapsView"];
type Caps = AdminSchemas["HouseCaps"];
type Held = ReturnType<typeof holdings>;

/** shareOf is a holding's absolute value as a share of a cap, e.g. "25.3%"; empty without a cap. */
function shareOf(value: string, cap: string): string {
  if (!dec.isDecimal(cap) || !dec.gt(cap, "0")) return "";
  return `${dec.div(dec.mul(dec.abs(value), "100"), cap, 1)}%`;
}

export const houseCapsKey = ["admin", "house", "caps"];

/** valuesOf reads a request's caps (as JSON in its payload); none when unreadable. */
function valuesOf(raw?: string): Record<string, string> {
  try {
    return JSON.parse(raw ?? "{}") as Record<string, string>;
  } catch {
    return {};
  }
}

/** CapValue is a cap with its unit: USDT, or times for the leverage; a level cap of zero is "no cap". */
function CapValue({ name, value, className }: { name: CapName; value: string | undefined; className?: string }) {
  const { t } = useTranslation();
  if (name === "level" && value !== undefined && dec.isDecimal(value) && dec.isZero(value)) {
    return <span className={className}>{t("admin.house.caps.noCap")}</span>;
  }
  return <Num value={value} unit={name === "contract_leverage" ? t("admin.house.caps.times") : "USDT"} className={className} />;
}

/** CapMove is one cap from and to, with what the move does. */
function CapMove({ name, before, after }: { name: CapName; before: string | undefined; after: string | undefined }) {
  const { t } = useTranslation();
  const d = before !== undefined && after !== undefined ? direction(name, before, after) : null;
  return (
    <span className="flex flex-col text-xs" data-testid={`house-cap-move-${name}`}>
      <span>
        <span className="text-fg-3">{t(`admin.house.caps.items.${name}.name`)}</span> <CapValue name={name} value={before} /> →{" "}
        <CapValue name={name} value={after} className="font-medium text-fg-1" />
      </span>
      {d && (
        <span className={d === "lower" ? "text-fg-3" : "text-warn"}>
          {t(`admin.house.caps.${d}`)}
          {t(`admin.house.caps.items.${name}.${d}`)}
        </span>
      )}
    </span>
  );
}

/** HouseCapsChange says what a HOUSE_CAPS request changes: each cap from and to, with what that does. */
export function HouseCapsChange({ a }: { a: Approval }) {
  const p = a.payload as Record<string, string>;
  const before = valuesOf(p.previous);
  const after = valuesOf(p.caps);
  return (
    <span className="flex flex-col gap-1" data-testid="house-caps-change">
      {HOUSE_CAPS.filter((f) => f in after).map((f) => (
        <CapMove key={f} name={f} before={before[f]} after={after[f]} />
      ))}
    </span>
  );
}

/**
 * HouseCapsCard shows HOUSE's caps, each with what it is for, and asks to
 * change them; assets is HOUSE's inventory valued at the last prices (the
 * page's GET /admin/v1/house), what the per-asset and total caps hold.
 */
export function HouseCapsCard({ admin, assets }: { admin: Admin; assets?: readonly { asset: string; value_usdt?: string | null }[] }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: houseCapsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/house/caps")),
    refetchInterval: 30_000,
  });
  const held = useMemo(() => (assets ? holdings(assets) : undefined), [assets]);
  const v = q.data;
  // The leverage cap's bound: the contracts' highest leverage (A97).
  const leverageMax = v?.contract_leverage_max ?? LEVERAGE_CEILING;
  return (
    <Card
      title={t("admin.house.caps.title")}
      extra={v && can(admin, "ledger.adjust.request") && !v.pending && <RequestCaps caps={v.caps} held={held} leverageMax={leverageMax} />}
    >
      <p className="mb-3 text-xs text-fg-3">{t("admin.house.caps.hint")}</p>
      {q.isError ? (
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : !v ? (
        <Skeleton className="h-48 w-full" />
      ) : (
        <div className="flex flex-col gap-3">
          <div data-testid="house-caps">
            <SummaryTable label={t("admin.house.caps.title")} noStatus headings={{ item: t("admin.summary.caps.cap"), summary: t("admin.house.caps.current") }}>
              {HOUSE_CAPS.map((f) => (
                <CapRow key={f} name={f} value={v.caps[f]} initial={v.initial?.[f]} held={held} leverageMax={leverageMax} />
              ))}
            </SummaryTable>
          </div>
          <p className="text-xs text-fg-3">
            {t("admin.house.caps.version", {
              version: v.caps.version,
              by: v.caps.updated_by === "environment" ? t("admin.house.caps.environment") : v.caps.updated_by,
            })}
            <TimeText value={v.caps.updated_at} />
          </p>
          {v.pending && <Pending a={v.pending} />}
          <History changes={v.changes} audit={can(admin, "audit.read")} />
        </div>
      )}
    </Card>
  );
}

/** HeldText is a holding (an asset's, or the total) in USDT with its share of a cap, red at or beyond it. */
function HeldText({ asset, value, cap }: { asset?: string; value: string; cap: string }) {
  const share = shareOf(value, cap);
  const beyond = dec.isDecimal(cap) && dec.gt(cap, "0") && dec.gte(dec.abs(value), cap);
  return (
    <span className={beyond ? "text-danger-strong" : undefined}>
      {asset && <span className="mr-1 font-medium">{asset}</span>}
      <Num value={value} decimals={2} unit="USDT" className={beyond ? "text-danger-strong" : undefined} />
      {share && <span className="ml-1">({share})</span>}
    </span>
  );
}

/**
 * CapRow is one cap (A94): its name over its purpose, its value now with
 * what HOUSE holds against it (the per-asset and total caps); opened, its
 * key and unit, range, first value and what lowering or raising it does.
 */
function CapRow({ name, value, initial, held, leverageMax }: {
  name: CapName; value: string; initial: string | undefined; held: Held | undefined; leverageMax: string;
}) {
  const { t } = useTranslation();
  const item = (k: string) => t(`admin.house.caps.items.${name}.${k}`, { max: formatDecimal(leverageMax) });
  const moved = initial !== undefined && dec.isDecimal(initial) && dec.isDecimal(value) && !dec.eq(initial, value);
  return (
    <SummaryRow
      data-testid={`house-cap-${name}`}
      title={item("name")}
      source={<span data-testid={`house-cap-${name}-purpose`}>{item("purpose")}</span>}
      summary={
        <span className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <span className="font-semibold" data-testid={`house-cap-${name}-value`}>
            <CapValue name={name} value={value} />
          </span>
          {moved && (
            <span className="text-xs text-fg-3" title={t("admin.house.caps.initialHint")}>
              {t("admin.house.caps.initial")} <CapValue name={name} value={initial} />
            </span>
          )}
          {held && name === "symbol" && (
            <span className="text-xs" data-testid="house-cap-symbol-held" title={t("admin.house.caps.heldHint")}>
              <span className="text-fg-3">{t("admin.house.caps.largest")} </span>
              {held.list[0] ? <HeldText asset={held.list[0].asset} value={held.list[0].value} cap={value} /> : <span className="text-fg-3">{t("admin.house.caps.noHolding")}</span>}
            </span>
          )}
          {held && name === "total" && (
            <span className="text-xs" data-testid="house-cap-total-held" title={t("admin.house.caps.heldHint")}>
              <span className="text-fg-3">{t("admin.house.caps.together")} </span>
              <HeldText value={held.total} cap={value} />
            </span>
          )}
        </span>
      }
      details={
        <Lines
          items={[
            [t("admin.summary.caps.key"), <KeyTag key="k">{name}</KeyTag>],
            [t("admin.summary.caps.unit"), name === "contract_leverage" ? t("admin.house.caps.times") : "USDT"],
            [t("admin.house.caps.range"), item("range")],
            [
              <span key="i" title={t("admin.house.caps.initialHint")}>
                {t("admin.house.caps.initial")}
              </span>,
              initial === undefined ? <span className="text-fg-3">{t("admin.house.caps.initialUnknown")}</span> : <CapValue name={name} value={initial} />,
            ],
            [t("admin.summary.caps.lower"), item("lower")],
            [t("admin.summary.caps.raise"), item("raise")],
          ]}
        />
      }
    />
  );
}

/** Pending is the request that waits, with what it changes. */
function Pending({ a }: { a: Approval }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-start gap-2 rounded-2 border border-warn px-3 py-2 text-sm" data-testid="house-caps-pending">
      <Badge tone="warn">{t("admin.house.caps.pending")}</Badge>
      <HouseCapsChange a={a} />
      <span className="text-xs text-fg-3">
        {t("admin.house.caps.pendingBy", { who: a.requested_by_email })} · <TimeText value={a.created_at} />
      </span>
    </div>
  );
}

/** History lists market-maker's latest changes of the caps, with every record in the audit log a click away. */
function History({ changes, audit }: { changes: View["changes"]; audit: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5">
      <span className="flex items-center gap-2 text-xs font-medium text-fg-2">
        {t("admin.house.caps.history")}
        {audit && (
          <Link to="/audit?target=house%3Acaps" className="font-normal text-brand" data-testid="house-caps-audit">
            {t("admin.house.caps.allRecords")}
          </Link>
        )}
      </span>
      {changes.length === 0 ? (
        <span className="text-xs text-fg-3">{t("admin.house.caps.noHistory")}</span>
      ) : (
        changes.map((c) => (
          <div key={c.version} className="flex flex-wrap items-start gap-x-3 gap-y-1 border-t border-line-1 pt-1.5 text-xs" data-testid="house-caps-version">
            <span className="font-mono text-fg-3">v{c.version}</span>
            <TimeText value={c.at} />
            {c.previous ? (
              HOUSE_CAPS.filter((f) => c.previous?.[f] !== c.caps[f]).map((f) => (
                <CapMove key={f} name={f} before={c.previous?.[f]} after={c.caps[f]} />
              ))
            ) : (
              <span className="text-fg-3">{t("admin.house.caps.firstVersion")}</span>
            )}
            <span className="text-fg-3">
              {c.actor === "environment" ? t("admin.house.caps.environment") : c.actor}
              {c.approver && ` / ${c.approver}`}
              {c.previous && c.reason && ` · ${c.reason}`}
            </span>
            {c.signed_by === "admin" && (
              <Badge tone="success" title={t("admin.house.caps.consoleHint")}>
                {t("admin.house.caps.console")}
              </Badge>
            )}
            {c.signed_by === "ops" && (
              <Badge tone="warn" title={t("admin.house.caps.opsHint")}>
                {t("admin.house.caps.ops")}
              </Badge>
            )}
          </div>
        ))
      )}
    </div>
  );
}

/**
 * RequestCaps asks a second administrator to change the caps that differ,
 * each from and to with what that does, in red where the per-asset or
 * total cap would be below what HOUSE holds (review R18: it stops buying).
 */
function RequestCaps({ caps, held, leverageMax }: { caps: Caps; held: Held | undefined; leverageMax: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const initial = () => Object.fromEntries(HOUSE_CAPS.map((f) => [f, caps[f]])) as Record<CapName, string>;
  const [values, setValues] = useState(initial);
  const max = formatDecimal(leverageMax);
  const range = (f: CapName) => t(`admin.house.caps.items.${f}.range`, { max });
  const ok = (f: CapName) => inRange(f, values[f], leverageMax) && stepOK(caps[f], values[f]);
  const changed = HOUSE_CAPS.filter((f) => ok(f) && !dec.eq(values[f].trim(), caps[f]));
  const bad = HOUSE_CAPS.some((f) => !ok(f));
  const symbolOver: Holding[] = held && changed.includes("symbol") ? over(held.list, values.symbol) : [];
  const totalBeyond = !!held && changed.includes("total") && totalOver(held.total, values.total);
  // How far one change can move a cap from its value now.
  const stepError = (f: CapName) => {
    const r = stepRange(f, caps[f], leverageMax);
    return r ? t("admin.house.caps.stepTo", { min: formatDecimal(r.min), max: formatDecimal(r.max) }) : t("admin.house.caps.step");
  };
  return (
    <FundAction
      trigger={(open) => (
        <Button
          size="sm"
          onClick={() => {
            setValues(initial());
            open();
          }}
          data-testid="house-caps-request"
        >
          {t("admin.house.caps.request")}
        </Button>
      )}
      danger={false}
      title={t("admin.house.caps.requestTitle")}
      description={t("admin.house.caps.requestHint")}
      target={<span className="font-medium">HOUSE</span>}
      confirmWord="HOUSE"
      disabled={bad || changed.length === 0}
      run={async (reason) => {
        try {
          return adminData(
            await adminApi.POST("/admin/v1/house/caps", {
              body: { caps: Object.fromEntries(changed.map((f) => [f, values[f].trim()])), version: caps.version, reason },
            }),
          );
        } catch (err) {
          // The caps moved or a request waits (409): the card reads them again.
          if (err instanceof ApiError && err.status === 409) void qc.invalidateQueries({ queryKey: houseCapsKey });
          throw err;
        }
      }}
      onDone={() => void qc.invalidateQueries({ queryKey: houseCapsKey })}
    >
      <div className="grid gap-2 sm:grid-cols-2">
        {HOUSE_CAPS.map((f) => (
          <label key={f} className="flex flex-col gap-1 text-xs text-fg-2">
            {t("admin.house.caps.withRange", { name: t(`admin.house.caps.items.${f}.name`), range: range(f) })}
            <Input
              size="sm"
              value={values[f]}
              inputMode="decimal"
              unit={f === "contract_leverage" ? t("admin.house.caps.times") : "USDT"}
              error={
                !inRange(f, values[f], leverageMax)
                  ? t("admin.house.caps.outOfRange", { range: range(f) })
                  : !stepOK(caps[f], values[f])
                    ? stepError(f)
                    : undefined
              }
              onValueChange={(v) => setValues({ ...values, [f]: v })}
              aria-label={f}
            />
          </label>
        ))}
      </div>
      <p className="text-xs text-fg-3" data-testid="house-caps-step-hint">
        {t("admin.house.caps.stepHint", { max })}
      </p>
      {!bad && changed.length === 0 ? (
        <p className="text-xs text-fg-3">{t("admin.house.caps.nothingChanges")}</p>
      ) : (
        changed.length > 0 && (
          <div className="flex flex-col gap-1.5 rounded-2 bg-bg-2 px-3 py-2" data-testid="house-caps-preview">
            <span className="text-xs font-medium text-fg-2">{t("admin.house.caps.changes")}</span>
            {changed.map((f) => (
              <CapMove key={f} name={f} before={caps[f]} after={values[f].trim()} />
            ))}
          </div>
        )
      )}
      {symbolOver.length > 0 && (
        <div role="alert" className="flex flex-col gap-1 rounded-2 border border-danger bg-danger/10 px-3 py-2 text-xs text-danger-strong" data-testid="house-caps-over-symbol">
          <span className="font-medium">{t("admin.house.caps.symbolOver")}</span>
          <span className="flex flex-wrap gap-x-3 gap-y-0.5">
            {symbolOver.map((h) => (
              <HeldText key={h.asset} asset={h.asset} value={h.value} cap={values.symbol.trim()} />
            ))}
          </span>
        </div>
      )}
      {totalBeyond && held && (
        <div role="alert" className="flex flex-col gap-1 rounded-2 border border-danger bg-danger/10 px-3 py-2 text-xs text-danger-strong" data-testid="house-caps-over-total">
          <span className="font-medium">{t("admin.house.caps.totalOver")}</span>
          <HeldText value={held.total} cap={values.total.trim()} />
        </div>
      )}
    </FundAction>
  );
}
