import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { authApi, unwrap } from "../api/client";
import type { components } from "../api/gen/auth";
import { stepUpHeaders } from "../auth/stepup";
import { selectSignedIn, useSession } from "../session/store";

// Devices and sign-ins (requirements §5.2): the live sessions, one of them
// this browser, and the login history. Ending another device's session or
// every other one needs a step-up.

export type DeviceSession = components["schemas"]["Session"];
export type LoginEvent = components["schemas"]["LoginEvent"];

export const sessionsKey = ["user", "sessions"] as const;
export const loginHistoryKey = ["user", "login-history"] as const;

/** useSessions lists the live sessions: this one first, then the most recently seen. */
export function useSessions() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: sessionsKey,
    queryFn: async () => sortSessions((await unwrap(authApi.GET("/v1/auth/sessions"))).sessions),
    enabled: signedIn,
    staleTime: 30_000,
  });
}

/** sortSessions puts the current session first, then the most recently seen. */
export function sortSessions(list: readonly DeviceSession[]): DeviceSession[] {
  return [...list].sort((a, b) => Number(b.current) - Number(a.current) || Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at));
}

/** useLoginHistory pages through the sign-ins, newest first. */
export function useLoginHistory(pageSize = 20) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: [...loginHistoryKey, pageSize],
    queryFn: ({ pageParam }) =>
      unwrap(authApi.GET("/v1/auth/login-history", { params: { query: { cursor: pageParam || undefined, limit: pageSize } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
  });
}

/** revokeSession ends another device's session (step-up needed); the current one is a sign-out. */
export async function revokeSession(sessionId: string, stepUpToken?: string): Promise<void> {
  await unwrap(
    authApi.DELETE("/v1/auth/sessions/{session_id}", {
      params: { path: { session_id: sessionId }, header: stepUpToken ? stepUpHeaders(stepUpToken) : {} },
    }),
  );
}

/** revokeOtherSessions ends every session but this one. */
export async function revokeOtherSessions(stepUpToken: string): Promise<void> {
  await unwrap(authApi.POST("/v1/auth/logout/all", { params: { header: stepUpHeaders(stepUpToken) } }));
}

export type DeviceKind = "desktop" | "mobile" | "tablet" | "unknown";

export type DeviceInfo = { kind: DeviceKind; browser: string; os: string };

// Order matters: Edge and Opera also say "Chrome", Chrome also says "Safari".
const BROWSERS: [RegExp, string][] = [
  [/Edg(?:e|A|iOS)?\/(\d+)/, "Edge"],
  [/OPR\/(\d+)/, "Opera"],
  [/SamsungBrowser\/(\d+)/, "Samsung Internet"],
  [/MicroMessenger\/(\d+)/, "WeChat"],
  [/(?:Firefox|FxiOS)\/(\d+)/, "Firefox"],
  [/HeadlessChrome\/(\d+)/, "Headless Chrome"],
  [/(?:Chrome|CriOS)\/(\d+)/, "Chrome"],
  [/Version\/(\d+)[\d.]*.*Safari\//, "Safari"],
];

const SYSTEMS: [RegExp, string][] = [
  [/Windows NT/, "Windows"],
  [/iPad/, "iPadOS"],
  [/iPhone|iPod/, "iOS"],
  [/Android/, "Android"],
  [/CrOS/, "ChromeOS"],
  [/Macintosh|Mac OS X/, "macOS"],
  [/Linux/, "Linux"],
];

/**
 * describeDevice reads a user agent well enough to name a session:
 * browser and major version, system, and the kind of device. Unknown
 * parts are "".
 */
export function describeDevice(userAgent: string, clientType = "WEB"): DeviceInfo {
  const ua = userAgent ?? "";
  const os = SYSTEMS.find(([re]) => re.test(ua))?.[1] ?? "";
  let browser = "";
  for (const [re, name] of BROWSERS) {
    const m = re.exec(ua);
    if (m) {
      browser = `${name} ${m[1]}`;
      break;
    }
  }
  let kind: DeviceKind = "unknown";
  if (/iPad|Tablet/.test(ua) || (/Android/.test(ua) && !/Mobile/.test(ua))) kind = "tablet";
  else if (/Mobi|iPhone|iPod/.test(ua)) kind = "mobile";
  else if (os) kind = "desktop";
  if (kind === "unknown" && clientType === "APP") kind = "mobile";
  return { kind, browser, os };
}

/** deviceName joins the known parts: "Chrome 128 · macOS" ("" when nothing is known). */
export function deviceName(info: DeviceInfo): string {
  return [info.browser, info.os].filter(Boolean).join(" · ");
}

/** A sign-in with the key the table rows use (the API gives sign-ins no ID). */
export type HistoryRow = LoginEvent & { key: string };

/** historyKey identifies a sign-in: its time, device, method, result and IP. */
export function historyKey(e: LoginEvent): string {
  return [e.created_at, e.device_id, e.method, e.result, e.ip].join("|");
}

/** flattenHistory joins pages of sign-ins in order, each once. */
export function flattenHistory(pages: readonly { items: readonly LoginEvent[] }[]): HistoryRow[] {
  const seen = new Set<string>();
  const rows: HistoryRow[] = [];
  for (const p of pages) {
    for (const e of p.items) {
      const key = historyKey(e);
      if (seen.has(key)) continue;
      seen.add(key);
      rows.push({ ...e, key });
    }
  }
  return rows;
}
