import { useQuery } from "@tanstack/react-query";
import { authApi, unwrap } from "../api/client";
import { ApiError } from "../api/errors";
import { deviceId } from "../session/store";
import type { Tokens } from "./login";

// Sign-up (requirements §6.1): the email or phone number is proven with a
// REGISTER code, then register/complete sets the password and accepts the
// current terms. The region comes from the phone number, or from
// `country` for email sign-ups. The flow below is shared by both sites.

export type Terms = { terms_version: string; risk_disclosure_version: string };

/** The query key of the current terms (public data, not a private root). */
export const termsKey = ["auth", "terms"] as const;

/** useTerms loads the document versions sign-up must accept. */
export function useTerms() {
  return useQuery({ queryKey: termsKey, queryFn: () => unwrap(authApi.GET("/v1/auth/terms")), staleTime: 10 * 60_000 });
}

/** termsFrom reads the current versions out of an AUTH_TERMS_OUTDATED error. */
export function termsFrom(err: unknown): Terms | null {
  if (!(err instanceof ApiError) || err.code !== "AUTH_TERMS_OUTDATED") return null;
  const { terms_version: t, risk_disclosure_version: r } = err.details;
  return typeof t === "string" && typeof r === "string" && t && r ? { terms_version: t, risk_disclosure_version: r } : null;
}

export type RegisterInput = {
  ticket: string;
  password: string;
  /** ISO 3166-1 alpha-2; the server uses it when the identifier has no region (emails). */
  country: string;
  language: string;
  timeZone: string;
  terms: Terms;
};

/** register creates the account behind a REGISTER ticket and returns the session's tokens. */
export function register(input: RegisterInput): Promise<Tokens> {
  return unwrap(
    authApi.POST("/v1/auth/register/complete", {
      body: {
        otp_ticket: input.ticket,
        password: input.password,
        country: input.country || undefined,
        language: input.language || undefined,
        timezone: input.timeZone || undefined,
        terms_version: input.terms.terms_version,
        risk_disclosure_version: input.terms.risk_disclosure_version,
        device_id: deviceId(),
      },
    }),
  );
}

// ISO 3166-1 alpha-2: every region may sign up (the project assumes a
// licence everywhere); names come from Intl.DisplayNames in the UI language.
export const REGIONS: readonly string[] = (
  "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ " +
  "CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR " +
  "GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP " +
  "KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT " +
  "MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW " +
  "SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG " +
  "UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW"
).split(" ");

const REGION_SET = new Set(REGIONS);

/** The region when nothing hints at one. */
export const DEFAULT_REGION = "SG";

// Zones whose region the language tag may not carry ("zh", "en").
const ZONE_REGIONS: Record<string, string> = {
  "Asia/Shanghai": "CN", "Asia/Chongqing": "CN", "Asia/Urumqi": "CN", "Asia/Hong_Kong": "HK", "Asia/Macau": "MO",
  "Asia/Taipei": "TW", "Asia/Singapore": "SG", "Asia/Tokyo": "JP", "Asia/Seoul": "KR", "Asia/Kuala_Lumpur": "MY",
  "Asia/Bangkok": "TH", "Asia/Jakarta": "ID", "Asia/Manila": "PH", "Asia/Ho_Chi_Minh": "VN", "Asia/Kolkata": "IN",
  "Asia/Calcutta": "IN", "Asia/Dubai": "AE", "Europe/London": "GB", "Europe/Paris": "FR", "Europe/Berlin": "DE",
  "Europe/Madrid": "ES", "Europe/Rome": "IT", "Europe/Amsterdam": "NL", "Europe/Zurich": "CH", "Europe/Istanbul": "TR",
  "Europe/Moscow": "RU", "America/New_York": "US", "America/Chicago": "US", "America/Denver": "US",
  "America/Los_Angeles": "US", "America/Toronto": "CA", "America/Vancouver": "CA", "America/Sao_Paulo": "BR",
  "America/Mexico_City": "MX", "Australia/Sydney": "AU", "Australia/Melbourne": "AU", "Pacific/Auckland": "NZ",
};

/**
 * guessRegion picks the likely region: the first language tag that names
 * one ("en-GB" → GB), else the time zone's, else DEFAULT_REGION.
 */
export function guessRegion(languages: readonly string[], timeZone = ""): string {
  for (const tag of languages) {
    for (const part of tag.split(/[-_]/).slice(1)) {
      const up = part.toUpperCase();
      if (up.length === 2 && REGION_SET.has(up)) return up;
    }
  }
  const byZone = ZONE_REGIONS[timeZone];
  return byZone ?? DEFAULT_REGION;
}

/** browserRegion guesses the visitor's region from the browser's languages and time zone. */
export function browserRegion(): string {
  const nav = globalThis.navigator;
  const languages = nav?.languages?.length ? nav.languages : nav?.language ? [nav.language] : [];
  let zone = "";
  try {
    zone = Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    // no Intl time zones: the language decides
  }
  return guessRegion(languages, zone);
}

const regionNames = new Map<string, Intl.DisplayNames | null>();

/** regionName is a region's name in a language ("SG" → 新加坡 / Singapore), or the code itself. */
export function regionName(code: string, locale: string): string {
  let names = regionNames.get(locale);
  if (names === undefined) {
    try {
      names = new Intl.DisplayNames([locale], { type: "region" });
    } catch {
      names = null;
    }
    regionNames.set(locale, names);
  }
  try {
    return names?.of(code) ?? code;
  } catch {
    return code;
  }
}

// The sign-up page's flow: the form (identifier, password, region,
// terms), then the code, then the request. A REGISTER ticket works once
// for 5 minutes; a request that fails before spending it (a weak
// password, outdated terms) keeps it, so fixing the form re-submits
// without a new code.

/** How long a ticket is trusted here: its 5 minutes less a margin for the round trip. */
export const TICKET_FRESH_MS = 5 * 60_000 - 30_000;

export type RegisterStep = "form" | "verify" | "submitting";

export type RegisterState = {
  step: RegisterStep;
  /** The normalized identifier the code goes (went) to. */
  identifier: string;
  ticket: string;
  /** Epoch ms the ticket arrived. */
  ticketAt: number;
  /** Bumped when the code step must start over (a spent or wrong ticket). */
  attempt: number;
};

export const initialRegisterState: RegisterState = { step: "form", identifier: "", ticket: "", ticketAt: 0, attempt: 0 };

export type RegisterAction =
  /** The form checks out for this identifier at `now`. */
  | { type: "continue"; identifier: string; now: number }
  /** The code checked out. */
  | { type: "ticket"; ticket: string; now: number }
  /** register/complete failed. */
  | { type: "failed"; error: unknown }
  /** Back from the code to the form. */
  | { type: "back" };

/** ticketFresh reports whether the kept ticket may still be sent for this identifier. */
export function ticketFresh(s: RegisterState, identifier: string, now: number): boolean {
  return s.ticket !== "" && s.identifier === identifier && now - s.ticketAt < TICKET_FRESH_MS;
}

export function registerReducer(s: RegisterState, a: RegisterAction): RegisterState {
  switch (a.type) {
    case "continue":
      if (ticketFresh(s, a.identifier, a.now)) return { ...s, step: "submitting" };
      return { ...s, step: "verify", identifier: a.identifier, ticket: "", ticketAt: 0 };
    case "ticket":
      return { ...s, step: "submitting", ticket: a.ticket, ticketAt: a.now };
    case "failed": {
      const code = a.error instanceof ApiError ? a.error.code : "";
      if (code === "AUTH_TICKET_INVALID" || code.startsWith("AUTH_OTP_")) {
        return { ...s, step: "verify", ticket: "", ticketAt: 0, attempt: s.attempt + 1 };
      }
      // Taken: the ticket's identifier cannot sign up at all.
      if (code === "AUTH_IDENTITY_TAKEN") return { ...s, step: "form", ticket: "", ticketAt: 0 };
      return { ...s, step: "form" };
    }
    case "back":
      return { ...s, step: "form" };
  }
}
