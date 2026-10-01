import { enumLabel, errorText, routes, selectUserId, useSession, useTotpStatus } from "@exchange/core";
import { useProfile } from "@exchange/core/user/profile";
import { securitySummary, useBoundIdentities, type SecurityLevel } from "@exchange/core/user/security";
import { Avatar, Badge, Skeleton, copyText, listItem, toast, type BadgeTone } from "@exchange/ui";
import { ChevronRight, Copy, RotateCcw, ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { primaryIdentity, shortId, totpState } from "../parts/logic";

const levelTone: Record<SecurityLevel, BadgeTone> = { low: "danger", medium: "warn", high: "success" };

function statusTone(status: string): BadgeTone {
  if (status === "ACTIVE") return "success";
  return status === "CLOSED" ? "neutral" : "warn";
}

/**
 * IdentityCard (design §7.3 ②): the avatar in a turning brand ring, the
 * masked identity, the UID to copy, and tags for the account status, the
 * security level and the simulated funds, on a grid that fades out from a
 * corner with two slowly drifting lights. "Edit profile" opens settings.
 */
export function IdentityCard() {
  const { t } = useTranslation();
  const userId = useSession(selectUserId);
  const ids = useBoundIdentities();
  const profile = useProfile();
  const totp = useTotpStatus();
  const identity = primaryIdentity(ids.data);
  const status = profile.data?.status;
  const level =
    ids.data && profile.data && totp.data
      ? securitySummary({
          totp: totpState(totp.data) === "on",
          email: Boolean(ids.data.EMAIL),
          phone: Boolean(ids.data.PHONE),
          antiPhishing: Boolean(profile.data.anti_phishing_code),
        }).level
      : null;

  const copy = async () => {
    if (await copyText(userId)) toast.success(t("mAccount.me.uidCopied"));
  };

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={0}
      data-testid="me-identity"
      className="relative isolate overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-4"
    >
      <div aria-hidden className="me-grid pointer-events-none absolute inset-0 -z-10" />
      <span
        aria-hidden
        className="pointer-events-none absolute -right-14 -top-20 -z-10 size-48 animate-[me-drift-a_20s_ease-in-out_infinite_alternate] rounded-full bg-brand/25 blur-3xl will-change-transform"
      />
      <span
        aria-hidden
        className="pointer-events-none absolute -bottom-24 -left-12 -z-10 size-44 animate-[me-drift-b_20s_ease-in-out_infinite_alternate] rounded-full bg-info/20 blur-3xl will-change-transform"
      />

      <div className="flex items-start gap-3.5">
        <span className="relative grid size-16 shrink-0 place-items-center">
          <span
            aria-hidden
            className="absolute inset-0 animate-[spin_8s_linear_infinite] rounded-full bg-[conic-gradient(from_0deg,var(--brand),transparent_35%,var(--brand)_65%,transparent_90%,var(--brand))] will-change-transform"
          />
          <span className="relative grid size-[58px] place-items-center rounded-full bg-bg-1">
            <Avatar name={identity ?? userId} size={52} />
          </span>
        </span>
        <div className="min-w-0 flex-1 pt-1.5">
          <div className="flex min-h-7 min-w-0 items-center gap-1">
            {ids.isPending ? (
              <Skeleton className="h-5 w-40 max-w-full" />
            ) : (
              <span className="min-w-0 truncate text-md font-semibold text-fg-1">{identity ?? t("mAccount.me.user")}</span>
            )}
            {ids.isError && (
              // The identity could not be read: the fallback name shows, with a way to try again.
              <button
                type="button"
                aria-label={`${t("common.retry")}: ${errorText(ids.error)}`}
                onClick={() => void ids.refetch()}
                className="-my-2 grid size-11 shrink-0 place-items-center text-danger"
              >
                <RotateCcw size={16} />
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={() => void copy()}
            aria-label={`${t("mAccount.me.copyUid")}: ${userId}`}
            className="-ml-1 inline-flex min-h-9 items-center gap-1.5 rounded-2 px-1 text-sm text-fg-3 transition-colors active:text-fg-1"
          >
            {t("mAccount.uid")}
            <span className="text-fg-2 tabular-nums">{shortId(userId)}</span>
            <Copy size={14} aria-hidden />
          </button>
        </div>
        <Link
          to={routes.settings}
          className="-mr-2 -mt-1.5 flex h-11 shrink-0 items-center gap-0.5 px-2 text-xs text-fg-3 transition-colors active:text-fg-1"
        >
          {t("mAccount.me.editProfile")}
          <ChevronRight size={14} aria-hidden />
        </Link>
      </div>

      <div className="mt-3 flex min-h-6 flex-wrap items-center gap-1.5">
        {status && (
          <Badge tone={statusTone(status)} dot>
            {enumLabel(status, "accountStatus")}
          </Badge>
        )}
        {level && (
          <Badge tone={levelTone[level]} icon={<ShieldCheck size={12} />}>
            {t("mAccount.me.securityTag", { level: t(`mAccount.security.levels.${level}`) })}
          </Badge>
        )}
        <Badge tone="brand">{t("mAccount.me.simulated")}</Badge>
      </div>
    </motion.section>
  );
}

/** IdentitySkeleton holds the card's place while a session may come back. */
export function IdentitySkeleton() {
  return (
    <div aria-busy className="rounded-3 border border-line-1 bg-bg-1 p-4">
      <div className="flex items-center gap-3.5">
        <Skeleton round className="size-16" />
        <div className="flex flex-1 flex-col gap-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-32" />
        </div>
      </div>
      <Skeleton className="mt-3 h-6 w-48" />
    </div>
  );
}
