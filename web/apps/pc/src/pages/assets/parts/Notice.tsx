import { ApiError, errorText } from "@exchange/core";
import { useEligibility, type Feature } from "@exchange/core/assets/hooks";
import { Button, cn } from "@exchange/ui";
import { CircleAlert, Info, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

const tones = {
  info: { box: "border-info/30 bg-info/10 text-info", icon: Info },
  warn: { box: "border-warn/40 bg-warn/10 text-warn", icon: TriangleAlert },
  danger: { box: "border-danger/40 bg-danger/10 text-danger", icon: CircleAlert },
} as const;

/** Notice is a coloured line of guidance: a warning, a refusal, a hint. */
export function Notice({ tone = "info", children, className, role }: { tone?: keyof typeof tones; children: ReactNode; className?: string; role?: "alert" | "status" }) {
  const { box, icon: Icon } = tones[tone];
  return (
    <div role={role} className={cn("flex items-start gap-2.5 rounded-2 border px-3.5 py-2.5 text-sm leading-relaxed", box, className)}>
      <Icon size={16} className="mt-0.5 shrink-0" />
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

/** reasonText localizes a refusal code such as USER_NOT_ELIGIBLE, like an API error. */
export function reasonText(code: string): string {
  return errorText(new ApiError(403, code, code));
}

/**
 * EligibilityNotice warns up front when the account may not use a
 * feature now (account status, or its switch is off for this user), so a
 * form does not fail only at the end. Renders nothing while allowed.
 */
export function EligibilityNotice({ feature, title }: { feature: Feature; title: string }) {
  const q = useEligibility(feature);
  if (!q.data || q.data.allowed) return null;
  return (
    <Notice tone="warn" role="alert" className="mb-4">
      <span className="font-medium">{title}</span>
      {" · "}
      {reasonText(q.data.reason_code || "USER_NOT_ELIGIBLE")}
    </Notice>
  );
}

/** useAllowed reports whether a feature is allowed (true until the answer says otherwise). */
export function useAllowed(feature: Feature): boolean {
  const q = useEligibility(feature);
  return !q.data || q.data.allowed;
}

/**
 * MoreError tells that loading further pages of a list failed (the list
 * itself still shows what it has) and offers to try again.
 */
export function MoreError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="alert" className="flex items-center justify-center gap-3 border-t border-line-1 px-4 py-3 text-sm text-fg-3">
      <CircleAlert size={14} className="shrink-0 text-danger" />
      <span className="min-w-0 truncate">{errorText(error)}</span>
      <Button size="sm" variant="secondary" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  );
}
