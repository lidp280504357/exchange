import { ApiError, errorText } from "@exchange/core";
import { channelOf, maskIdentifier, type IdentityKind, type ParsedIdentifier } from "@exchange/core/auth/identity";
import {
  bindIdentity, markBound, rebindIdentity, stepUpMethodFor, UNKNOWN_MASK, type BoundIdentities,
} from "@exchange/core/user/security";
import { Button, Dialog, Form, FormField, FormSubmit, Input, Stepper, toast, useZodForm, z } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { CircleCheck, Info, Mail, Smartphone } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { OtpStep } from "../../../features/auth/OtpStep";
import { parsedOrNull } from "../../auth/parts/fields";
import { identifierField } from "../../auth/parts/schemas";
import { StepUpInline } from "./StepUpInline";

export type IdentityTask = { kind: IdentityKind; action: "bind" | "rebind" };

/**
 * IdentityDialog binds a second email or phone, or replaces one
 * (requirements §6.4): a step-up through the identity that stays (or the
 * authenticator app), the new identity, then its BIND_IDENTITY or
 * REBIND_IDENTITY code. A single-identity account's rebind waits for
 * review.
 */
export function IdentityDialog({ task, bound, totp, onClose }: { task: IdentityTask | null; bound: BoundIdentities; totp: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  const title = task ? t(`pcAccount.identity.${task.action === "bind" ? "bindTitle" : "rebindTitle"}.${task.kind}`) : "";
  return (
    <Dialog open={task !== null} onOpenChange={(o) => !o && onClose()} title={title} size="md" persistent footer={null}>
      {task && <IdentityFlow task={task} bound={bound} totp={totp} onClose={onClose} />}
    </Dialog>
  );
}

function IdentityFlow({ task, bound, totp, onClose }: { task: IdentityTask; bound: BoundIdentities; totp: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const { kind, action } = task;
  const kindName = t(`pcAccount.kind.${kind}`);
  const [step, setStep] = useState(0);
  const [token, setToken] = useState("");
  const [target, setTarget] = useState<ParsedIdentifier | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState<unknown>(null);
  // Fixed for the flow: a refetch of the identities must not switch the channel mid-code.
  const [method] = useState(() => stepUpMethodFor(action, kind, bound, totp));
  const [stepUpMask] = useState(() => (method === "TOTP" ? undefined : bound[method === "SMS" ? "PHONE" : "EMAIL"]));
  const other: IdentityKind = kind === "EMAIL" ? "PHONE" : "EMAIL";
  const schema = useMemo(() => z.object({ identifier: identifierField(t, kind) }), [t, kind]);
  const form = useZodForm(schema, { defaultValues: { identifier: "" } });

  const finish = async (ticket: string) => {
    if (!target) return;
    setError(null);
    try {
      if (action === "bind") {
        await bindIdentity(ticket, token);
        markBound(qc, kind, maskIdentifier(target.value));
        toast.success(t("pcAccount.identity.bound", { kind: kindName }));
      } else if ((await rebindIdentity(ticket, token)) === "DONE") {
        markBound(qc, kind, maskIdentifier(target.value));
        toast.success(t("pcAccount.identity.rebound", { kind: kindName }));
      } else {
        toast.info(t("pcAccount.identity.pending"), { duration: 8000 });
      }
      onClose();
    } catch (e) {
      const code = e instanceof ApiError ? e.code : "";
      if (code === "AUTH_IDENTITY_KIND_BOUND") {
        // The page did not know this identity: remember that it exists.
        markBound(qc, kind, bound[kind] ?? UNKNOWN_MASK);
        toast.error(t("pcAccount.identity.kindBound", { kind: kindName }));
        onClose();
        return;
      }
      setError(e);
      if (code === "AUTH_STEP_UP_REQUIRED") {
        setToken("");
        setStep(0);
      } else if (code === "AUTH_IDENTITY_TAKEN") {
        setStep(1);
      } else {
        setAttempt((a) => a + 1); // the ticket is spent: a new code
      }
    }
  };

  return (
    <div className="flex flex-col gap-5">
      <Stepper
        size="sm"
        current={step}
        steps={[
          { key: "verify", title: t("pcAccount.identity.stepVerify") },
          { key: "new", title: t("pcAccount.identity.stepNew", { kind: kindName }) },
          { key: "confirm", title: t("pcAccount.identity.stepConfirm", { kind: kindName }) },
        ]}
      />
      {step === 0 && (
        <div className="flex flex-col gap-3">
          {action === "rebind" && method !== "TOTP" && bound[other] && (
            <p className="flex items-start gap-2 rounded-2 bg-info/10 p-3 text-xs leading-relaxed text-info">
              <Info size={14} className="mt-px shrink-0" />
              {t("pcAccount.identity.rebindRule", { kind: kindName })}
            </p>
          )}
          <StepUpInline
            method={method}
            mask={stepUpMask}
            onToken={(tok) => {
              setToken(tok);
              setError(null);
              setStep(1);
            }}
          />
        </div>
      )}
      {step === 1 && (
        <Form
          form={form}
          onSubmit={(v) => {
            setTarget(parsedOrNull(v.identifier, kind));
            setError(null);
            setStep(2);
          }}
          aria-label={t("pcAccount.identity.newLabel", { kind: kindName })}
        >
          <p className="flex items-center gap-2 text-sm text-success">
            <CircleCheck size={16} /> {t("pcAccount.identity.verified")}
          </p>
          <FormField
            label={t("pcAccount.identity.newLabel", { kind: kindName })}
            name="identifier"
            required
            hint={kind === "PHONE" ? t("pcAuth.phoneHint") : undefined}
          >
            <Input
              {...form.register("identifier")}
              autoFocus
              type={kind === "EMAIL" ? "email" : "tel"}
              autoComplete={kind === "EMAIL" ? "email" : "tel"}
              placeholder={kind === "EMAIL" ? t("pcAuth.emailPlaceholder") : t("pcAuth.phonePlaceholder")}
              prefix={kind === "EMAIL" ? <Mail size={16} /> : <Smartphone size={16} />}
            />
          </FormField>
          <FormSubmit block>{t("pcAuth.continue")}</FormSubmit>
        </Form>
      )}
      {step === 2 && target && (
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3 rounded-2 bg-bg-2 px-3 py-2 text-sm">
            <span className="min-w-0 truncate text-fg-2">{t("pcAccount.identity.confirmHint", { target: target.value })}</span>
            <Button size="sm" variant="ghost" onClick={() => setStep(1)}>
              {t("pcAuth.change")}
            </Button>
          </div>
          <OtpStep
            key={`${target.value}-${attempt}`}
            target={{ scene: action === "bind" ? "BIND_IDENTITY" : "REBIND_IDENTITY", channel: channelOf(kind), identifier: target.value }}
            sentTo={target.value}
            confirmLabel={t("pcAccount.identity.submit")}
            onTicket={(ticket) => void finish(ticket)}
          />
          <p className="text-xs text-fg-3">{t("pcAccount.identity.takenHint", { kind: kindName })}</p>
        </div>
      )}
      {error != null && (
        <p role="alert" className="text-sm text-danger">
          {errorCode(error) === "AUTH_STEP_UP_REQUIRED" ? t("pcAccount.identity.stepUpAgain") : errorText(error)}
        </p>
      )}
    </div>
  );
}

function errorCode(e: unknown): string {
  return e instanceof ApiError ? e.code : "";
}
