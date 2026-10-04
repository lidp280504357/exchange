import { useQuery, type QueryClient } from "@tanstack/react-query";
import { authApi, notificationApi, unwrap } from "../api/client";
import type { components } from "../api/gen/auth";
import { channelOf, kindOfMask, type IdentityKind } from "../auth/identity";
import type { OtpChannel } from "../auth/otp";
import { stepUpHeaders, totpKey } from "../auth/stepup";
import { qk } from "../query/keys";
import { deviceId, selectSignedIn, useSession } from "../session/store";
import type { Notice } from "./notifications";

// The security centre (requirements §6.4, §6.5): the authenticator app,
// the second identity, rebinding, and a summary of how well the account
// is protected. Both user sites use these.

// ---------------------------------------------------------------------------
// Bound identities.
//
// No endpoint lists the caller's email and phone number. The page reads
// them from what the server does report, newest first: the masked
// identity of each sign-in (login history, sign-up included) and the
// "contact added / changed" notices (IDENTITY_CHANGED, data.channel and
// data.new). An identity found in neither shows as not bound until it is
// used; binding one then fails with AUTH_IDENTITY_KIND_BOUND, which the
// page turns into "bound" (markBound).

/** The masked email and phone number of the account, when known. */
export type BoundIdentities = Partial<Record<IdentityKind, string>>;

type LoginEvent = components["schemas"]["LoginEvent"];

type Evidence = { kind: IdentityKind; mask: string; at: number };

/**
 * deriveIdentities picks, per kind, the most recent masked identity seen in
 * the login history and the identity notices.
 */
export function deriveIdentities(history: readonly Pick<LoginEvent, "identity" | "created_at">[], notices: readonly Pick<Notice, "type" | "data" | "created_at">[]): BoundIdentities {
  const seen: Evidence[] = [];
  for (const e of history) {
    const kind = kindOfMask(e.identity ?? "");
    if (kind) seen.push({ kind, mask: e.identity, at: Date.parse(e.created_at) || 0 });
  }
  for (const n of notices) {
    if (n.type !== "IDENTITY_CHANGED") continue;
    const channel = n.data?.channel;
    const mask = n.data?.new;
    if ((channel !== "EMAIL" && channel !== "SMS") || !mask) continue;
    seen.push({ kind: channel === "SMS" ? "PHONE" : "EMAIL", mask, at: Date.parse(n.created_at) || 0 });
  }
  const out: BoundIdentities = {};
  const at: Partial<Record<IdentityKind, number>> = {};
  for (const e of seen) {
    if (at[e.kind] === undefined || e.at > at[e.kind]!) {
      out[e.kind] = e.mask;
      at[e.kind] = e.at;
    }
  }
  return out;
}

/** The query key of the derived identities (a private root). */
export const identitiesKey = ["user", "identities"] as const;

/** useBoundIdentities derives the masked email and phone from the history and the notices. */
export function useBoundIdentities() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: identitiesKey,
    queryFn: async () => {
      const [history, notices] = await Promise.allSettled([
        unwrap(authApi.GET("/v1/auth/login-history", { params: { query: { limit: 200 } } })),
        unwrap(notificationApi.GET("/v1/notifications", { params: { query: { limit: 100 } } })),
      ]);
      if (history.status === "rejected" && notices.status === "rejected") throw history.reason;
      return deriveIdentities(
        history.status === "fulfilled" ? history.value.items : [],
        notices.status === "fulfilled" ? (notices.value.items as Notice[]) : [],
      );
    },
    enabled: signedIn,
    staleTime: 5 * 60_000,
  });
}

/** markBound records a bound identity in the cache (after binding, or when the server says one is bound). */
export function markBound(qc: QueryClient, kind: IdentityKind, mask: string): void {
  qc.setQueryData<BoundIdentities>(identitiesKey, (cur) => ({ ...cur, [kind]: mask }));
}

/** The mask shown for an identity the server says is bound but the page never saw. */
export const UNKNOWN_MASK = "••••";

// ---------------------------------------------------------------------------
// Binding and rebinding.

/** How a step-up is proven: the authenticator app, or a code by mail or SMS. */
export type StepUpMethod = "TOTP" | OtpChannel;

/**
 * stepUpMethodFor picks the step-up a binding needs (§6.4, §6.5): the
 * authenticator app when bound; else a code to the identity that stays.
 * Binding a kind means the account only has the other one. Rebinding with
 * both bound must step up through the other identity (the server rejects
 * the same channel); a single identity steps up with itself and the
 * request waits for review.
 */
export function stepUpMethodFor(action: "bind" | "rebind", kind: IdentityKind, bound: BoundIdentities, totp: boolean): StepUpMethod {
  if (totp) return "TOTP";
  const other: IdentityKind = kind === "EMAIL" ? "PHONE" : "EMAIL";
  if (action === "bind" || bound[other]) return channelOf(other);
  return channelOf(kind);
}

/** bindIdentity adds the identity proven by a BIND_IDENTITY ticket. */
export async function bindIdentity(ticket: string, stepUpToken: string): Promise<void> {
  await unwrap(
    authApi.POST("/v1/auth/identity/bind", { params: { header: stepUpHeaders(stepUpToken) }, body: { otp_ticket: ticket, device_id: deviceId() } }),
  );
}

/** rebindIdentity replaces an identity; a single-identity account's request waits for review. */
export async function rebindIdentity(ticket: string, stepUpToken: string): Promise<"DONE" | "PENDING_REVIEW"> {
  const res = await unwrap(
    authApi.POST("/v1/auth/identity/rebind", { params: { header: stepUpHeaders(stepUpToken) }, body: { otp_ticket: ticket, device_id: deviceId() } }),
  );
  return res.status;
}

// ---------------------------------------------------------------------------
// The authenticator app.

export type TotpSetup = { secret: string; otpauth_uri: string };

/** setupTotp starts binding an app (after a step-up): the secret and its otpauth link. */
export function setupTotp(stepUpToken: string): Promise<TotpSetup> {
  return unwrap(authApi.POST("/v1/auth/totp/setup", { params: { header: stepUpHeaders(stepUpToken) } }));
}

/** confirmTotp binds the app with its first code. */
export async function confirmTotp(code: string): Promise<void> {
  await unwrap(authApi.POST("/v1/auth/totp/confirm", { body: { code: code.trim() } }));
}

/** disableTotp removes the app; the step-up must have been proven with it. */
export async function disableTotp(stepUpToken: string): Promise<void> {
  await unwrap(authApi.DELETE("/v1/auth/totp", { params: { header: stepUpHeaders(stepUpToken) } }));
}

/** refreshTotp refetches the binding state after a change, and the withdrawal limits it moves. */
export async function refreshTotp(qc: QueryClient): Promise<void> {
  await Promise.all([qc.invalidateQueries({ queryKey: totpKey }), qc.invalidateQueries({ queryKey: qk.withdrawLimits })]);
}

/** groupSecret splits a base32 secret in fours for reading aloud and typing ("JBSW Y3DP ..."). */
export function groupSecret(secret: string): string {
  return (secret.replace(/\s+/g, "").match(/.{1,4}/g) ?? []).join(" ");
}

// ---------------------------------------------------------------------------
// The summary at the top of the security centre.

export type SecurityFactors = {
  totp: boolean;
  email: boolean;
  phone: boolean;
  antiPhishing: boolean;
};

export type SecurityLevel = "low" | "medium" | "high";

export type SecuritySummary = {
  score: number;
  max: number;
  level: SecurityLevel;
  /** What is left to do, most protective first. */
  missing: (keyof SecurityFactors)[];
};

// The password counts 1; each identity 1; the authenticator app 2 (it is
// the step-up of choice); the anti-phishing code 1.
const WEIGHTS: Record<keyof SecurityFactors, number> = { totp: 2, email: 1, phone: 1, antiPhishing: 1 };
const ORDER: (keyof SecurityFactors)[] = ["totp", "phone", "email", "antiPhishing"];

/** securitySummary scores the account: high needs the authenticator app and 5 of 6 points. */
export function securitySummary(f: SecurityFactors): SecuritySummary {
  const max = 1 + Object.values(WEIGHTS).reduce((a, b) => a + b, 0);
  const score = 1 + ORDER.reduce((n, k) => n + (f[k] ? WEIGHTS[k] : 0), 0);
  const level: SecurityLevel = score >= 5 && f.totp ? "high" : score >= 3 ? "medium" : "low";
  return { score, max, level, missing: ORDER.filter((k) => !f[k]) };
}
