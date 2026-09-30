import type { IdentityKind } from "@exchange/core/auth/identity";
import { z } from "@exchange/ui";
import { identifierProblemKey, type Translate } from "./fields";

// Zod rules of the shared fields; the messages come from mAuth.

/** identifierField validates an email or phone number (of one kind, or either). */
export function identifierField(t: Translate, kind?: IdentityKind) {
  return z.string().superRefine((v, ctx) => {
    const key = identifierProblemKey(v, kind);
    if (key) ctx.addIssue({ code: "custom", message: t(`mAuth.idProblem.${key}`) });
  });
}
