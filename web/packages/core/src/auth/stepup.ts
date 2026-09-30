import { useQuery } from "@tanstack/react-query";
import { authApi, unwrap } from "../api/client";
import { selectSignedIn, deviceId, useSession } from "../session/store";

// Step-up (requirements §6.5): a sensitive action (withdrawal, binding,
// password change, removing the authenticator) needs a token proving it
// is still the user, valid 10 minutes for one action. With an
// authenticator app bound only its code is accepted; otherwise a STEP_UP
// code by mail or SMS, traded for a ticket first (useOtp).

/** The query key of the authenticator binding (a private root, cleared on sign-out). */
export const totpKey = ["user", "totp"] as const;

/** useTotpStatus reports whether an authenticator app is bound (and whether one is pending). */
export function useTotpStatus() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({ queryKey: totpKey, queryFn: () => unwrap(authApi.GET("/v1/auth/totp")), enabled: signedIn, staleTime: 60_000 });
}

/** redeemStepUp trades a STEP_UP ticket or an authenticator code for a step-up token. */
export async function redeemStepUp(proof: { otpTicket: string } | { totpCode: string }): Promise<string> {
  const body = "otpTicket" in proof ? { otp_ticket: proof.otpTicket } : { totp_code: proof.totpCode };
  const res = await unwrap(authApi.POST("/v1/auth/step-up", { body: { ...body, device_id: deviceId() } }));
  return res.step_up_token;
}

/** The header a sensitive call carries its step-up token in. */
export const STEP_UP_HEADER = "X-Step-Up-Token";

/** stepUpHeaders is the params.header part of a call that needs a step-up token. */
export function stepUpHeaders(token: string): { "X-Step-Up-Token": string } {
  return { [STEP_UP_HEADER]: token } as { "X-Step-Up-Token": string };
}
