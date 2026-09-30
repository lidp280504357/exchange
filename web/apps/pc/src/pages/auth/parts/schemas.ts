import type { IdentityKind } from "@exchange/core/auth/identity";
import { passwordChecks } from "@exchange/core/auth/password";
import { z } from "@exchange/ui";
import { identifierProblemKey, type Translate } from "./fields";

// Zod rules of the shared fields; the messages come from pcAuth.

/** identifierField validates an email or phone number (of one kind, or either). */
export function identifierField(t: Translate, kind?: IdentityKind) {
  return z.string().superRefine((v, ctx) => {
    const key = identifierProblemKey(v, kind);
    if (key) ctx.addIssue({ code: "custom", message: t(`pcAuth.idProblem.${key}`) });
  });
}

/** newPasswordField validates a new password against the rules the page can judge. */
export function newPasswordField(t: Translate, identifiers: () => string[]) {
  return z.string().superRefine((v, ctx) => {
    const failed = passwordChecks(v, identifiers()).find((c) => !c.ok);
    if (failed) ctx.addIssue({ code: "custom", message: t(`pcAuth.rule.${failed.rule}`) });
  });
}
