import { useQuery, type QueryClient } from "@tanstack/react-query";
import { unwrap, userApi } from "../api/client";
import type { components } from "../api/gen/user";
import { stepUpHeaders } from "../auth/stepup";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";

// The caller's profile (api/openapi/user.yaml): account status, region,
// language, time zone and the anti-phishing code that every mail carries.

export type Profile = components["schemas"]["Profile"];

/** useProfile loads the caller's profile. */
export function useProfile() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({ queryKey: qk.profile, queryFn: () => unwrap(userApi.GET("/v1/user/profile")), enabled: signedIn, staleTime: 60_000 });
}

export type ProfilePatch = { language?: string; timezone?: string; anti_phishing_code?: string };

/**
 * updateProfile changes the language, time zone or anti-phishing code;
 * the code needs a step-up token (an empty code clears it).
 */
export function updateProfile(patch: ProfilePatch, stepUpToken?: string): Promise<Profile> {
  return unwrap(userApi.PATCH("/v1/user/profile", { body: patch, params: { header: stepUpToken ? stepUpHeaders(stepUpToken) : {} } }));
}

/**
 * avatarOf is the uploaded avatar to show at a size (design 2026-10-07,
 * avatars and usernames): the 64 px picture up to 32 px, the 256 px one
 * above; undefined for the built-in avatar.
 */
export function avatarOf(p: Pick<Profile, "avatar_url" | "avatar_thumb_url"> | undefined, size: number): string | undefined {
  if (!p?.avatar_url) return undefined;
  return (size <= 32 ? p.avatar_thumb_url : null) ?? p.avatar_url;
}

/** keepProfile puts an updated profile in the cache. */
export function keepProfile(qc: QueryClient, p: Profile): void {
  qc.setQueryData(qk.profile, p);
}

export const ANTI_PHISHING_MIN = 4;
export const ANTI_PHISHING_MAX = 20;

export type AntiPhishingProblem = "length" | "chars";

/** checkAntiPhishing mirrors the contract: 4 to 20 letters and digits, or empty to clear it. */
export function checkAntiPhishing(code: string): AntiPhishingProblem | null {
  if (code === "") return null;
  if (!/^[A-Za-z0-9]*$/.test(code)) return "chars";
  return code.length < ANTI_PHISHING_MIN || code.length > ANTI_PHISHING_MAX ? "length" : null;
}

/** maskCode hides all but the first two characters of a code ("Ab3x9" → "Ab***"). */
export function maskCode(code: string): string {
  if (!code) return "";
  return code.slice(0, 2) + "*".repeat(Math.max(3, code.length - 2));
}
