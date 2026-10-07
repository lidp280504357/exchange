import { ApiError, dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Input, Skeleton } from "@exchange/ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Num, TimeText } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { Card } from "../../kit/Page";
import { direction, HOUSE_CAPS, inRange, stepOK, type CapName } from "./capsRules";

// HOUSE's caps at run time (user 2026-10-07, A69; market-maker review C45):
// what HOUSE quotes within - each cap with its unit, current and first
// value, allowed range, purpose and what lowering or raising it does (user
// 06:0x) - with the request that waits and the latest changes; a change is
// a HOUSE_CAPS request a second administrator approves, its dialog listing
// each cap from and to with what that does.

type View = AdminSchemas["HouseCapsView"];
type Caps = AdminSchemas["HouseCaps"];

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

/** HouseCapsCard shows HOUSE's caps, each with what it is for, and asks to change them. */
export function HouseCapsCard({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: houseCapsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/house/caps")),
    refetchInterval: 30_000,
  });
  const v = q.data;
  return (
    <Card
      title={t("admin.house.caps.title")}
      extra={v && can(admin, "ledger.adjust.request") && !v.pending && <RequestCaps caps={v.caps} />}
    >
      <p className="mb-3 text-xs text-fg-3">{t("admin.house.caps.hint")}</p>
      {q.isError ? (
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : !v ? (
        <Skeleton className="h-48 w-full" />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="house-caps">
            {HOUSE_CAPS.map((f) => (
              <CapCell key={f} name={f} value={v.caps[f]} initial={v.initial?.[f]} />
            ))}
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

/** CapCell is one cap: its name, unit, value now and first, range, purpose and what moving it does. */
function CapCell({ name, value, initial }: { name: CapName; value: string; initial: string | undefined }) {
  const { t } = useTranslation();
  const item = (k: string) => t(`admin.house.caps.items.${name}.${k}`);
  return (
    <div className="flex flex-col gap-1.5 rounded-2 border border-line-1 px-3 py-2.5" data-testid={`house-cap-${name}`}>
      <div className="flex items-baseline gap-2">
        <span className="text-sm font-medium text-fg-1">{item("name")}</span>
        <span className="font-mono text-xs text-fg-3">{name}</span>
        <span className="ml-auto text-xs text-fg-3">{name === "contract_leverage" ? t("admin.house.caps.times") : "USDT"}</span>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
        <dt className="text-fg-3">{t("admin.house.caps.current")}</dt>
        <dd className="text-sm font-medium" data-testid={`house-cap-${name}-value`}>
          <CapValue name={name} value={value} />
        </dd>
        <dt className="text-fg-3" title={t("admin.house.caps.initialHint")}>
          {t("admin.house.caps.initial")}
        </dt>
        <dd>{initial === undefined ? <span className="text-fg-3">{t("admin.house.caps.initialUnknown")}</span> : <CapValue name={name} value={initial} />}</dd>
        <dt className="text-fg-3">{t("admin.house.caps.range")}</dt>
        <dd>{item("range")}</dd>
      </dl>
      <p className="text-xs text-fg-2" data-testid={`house-cap-${name}-purpose`}>
        {item("purpose")}
      </p>
      <p className="text-xs text-fg-3">
        <span className="text-fg-2">{t("admin.house.caps.lower")}</span>
        {item("lower")}
      </p>
      <p className="text-xs text-fg-3">
        <span className="text-fg-2">{t("admin.house.caps.raise")}</span>
        {item("raise")}
      </p>
    </div>
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

/** RequestCaps asks a second administrator to change the caps that differ, each from and to with what that does. */
function RequestCaps({ caps }: { caps: Caps }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const initial = () => Object.fromEntries(HOUSE_CAPS.map((f) => [f, caps[f]])) as Record<CapName, string>;
  const [values, setValues] = useState(initial);
  const ok = (f: CapName) => inRange(f, values[f]) && stepOK(caps[f], values[f]);
  const changed = HOUSE_CAPS.filter((f) => ok(f) && !dec.eq(values[f].trim(), caps[f]));
  const bad = HOUSE_CAPS.some((f) => !ok(f));
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
            {t("admin.house.caps.withRange", { name: t(`admin.house.caps.items.${f}.name`), range: t(`admin.house.caps.items.${f}.range`) })}
            <Input
              size="sm"
              value={values[f]}
              inputMode="decimal"
              unit={f === "contract_leverage" ? t("admin.house.caps.times") : "USDT"}
              error={
                !inRange(f, values[f])
                  ? t("admin.house.caps.outOfRange", { range: t(`admin.house.caps.items.${f}.range`) })
                  : !stepOK(caps[f], values[f])
                    ? t("admin.house.caps.step")
                    : undefined
              }
              onValueChange={(v) => setValues({ ...values, [f]: v })}
              aria-label={f}
            />
          </label>
        ))}
      </div>
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
    </FundAction>
  );
}
