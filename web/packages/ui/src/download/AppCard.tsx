import { fileSize, type AppDownload, type AppPlatform } from "@exchange/core/platform/apps";
import { Apple, Bot } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { Badge } from "../components/Badge";
import { CopyButton } from "../data/CopyButton";
import { TimeText } from "../data/TimeText";
import { cn } from "../lib/cn";
import { listItem } from "../lib/motion";

// One app of the download page (design 2026-10-07, App download page §4),
// as both sites show it: the strings come from the site (labels), the
// button, the QR code and the steps are the site's to choose.

export type AppCardLabels = {
  /** "Android", "iOS". */
  platform: string;
  /** How it is offered: "安装包（APK）", "App Store". */
  kind: string;
  version: string;
  updated: string;
  size: string;
  minOs: string;
  notes: string;
  /** The install steps' summary ("安装说明"). */
  help: string;
  /** A badge beside the platform (the phone's own: "本机"). */
  badge?: string;
  /** The SHA-256 copy button's name ("复制 SHA-256"). */
  copy?: string;
};

export type AppCardProps = {
  platform: AppPlatform;
  app: AppDownload;
  labels: AppCardLabels;
  /** The system it needs, in words ("Android 7.0 及以上"); none when the package does not say. */
  minOs?: string | null;
  /** The version notes in the user's language. */
  notes?: string;
  /** A QR code for a phone, with the line under it (the PC site's cards): beside the facts. */
  qr?: ReactNode;
  /** The buttons and links under the facts. */
  actions?: ReactNode;
  /** The steps to install a file that is not from a store. */
  steps?: string[] | null;
  /** The steps open from the start (the phone's own platform). */
  stepsOpen?: boolean;
  /** "wide": the QR code beside the facts (PC); "narrow": stacked, the values to the right (phone). */
  layout?: "wide" | "narrow";
  /** Its place on the page, for the entrance stagger. */
  index?: number;
  className?: string;
};

/**
 * AppCard is one app to download: the platform and how it is offered, the
 * facts of an uploaded file (version and build, date, size, the system it
 * needs, the SHA-256 to copy), its notes, the site's buttons and, for a
 * file not from a store, how to install it.
 */
export function AppCard({ platform, app, labels, minOs, notes, qr, actions, steps, stepsOpen, layout = "wide", index = 0, className }: AppCardProps) {
  const Icon = platform === "ios" ? Apple : Bot;
  const wide = layout === "wide";
  const facts = (
    <dl className={cn("grid min-w-0 content-start grid-cols-[auto_minmax(0,1fr)] text-sm", wide ? "flex-1 gap-x-4 gap-y-2" : "gap-x-4 gap-y-1.5")}>
      {app.version && (
        <Fact label={labels.version} right={!wide}>
          {app.version}
          {app.build ? <span className="text-fg-3"> ({app.build})</span> : null}
        </Fact>
      )}
      <Fact label={labels.updated} right={!wide}>
        <TimeText value={app.updated_at} format="date" />
      </Fact>
      {app.size !== null && (
        <Fact label={labels.size} right={!wide}>
          {fileSize(app.size)}
        </Fact>
      )}
      {minOs && (
        <Fact label={labels.minOs} right={!wide}>
          {minOs}
        </Fact>
      )}
      {app.sha256 && (
        <Fact label="SHA-256" right={!wide}>
          <span className={cn("flex items-start gap-1", !wide && "justify-end")}>
            <span className="break-all font-mono text-xs leading-5 text-fg-2">{app.sha256}</span>
            <CopyButton value={app.sha256} size={wide ? 12 : 14} label={labels.copy} />
          </span>
        </Fact>
      )}
    </dl>
  );
  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      aria-labelledby={`app-${platform}`}
      data-testid={`app-${platform}`}
      className={cn("flex flex-col rounded-3 bg-bg-1", wide ? "gap-5 border border-line-1 p-6" : "gap-4 p-4", className)}
    >
      <div className="flex items-center gap-3">
        <span aria-hidden className={cn("grid shrink-0 place-items-center rounded-3 bg-bg-2 text-fg-1", wide ? "size-12" : "size-11")}>
          <Icon size={wide ? 26 : 24} />
        </span>
        <div className="min-w-0 flex-1">
          <h2 id={`app-${platform}`} className={cn("flex items-center gap-2 font-semibold text-fg-1", wide ? "text-lg" : "text-md")}>
            {labels.platform}
            {labels.badge && <Badge tone="brand">{labels.badge}</Badge>}
          </h2>
          <p className="text-sm text-fg-3">{labels.kind}</p>
        </div>
      </div>
      {qr ? (
        <div className="flex gap-6">
          <div className="flex w-40 shrink-0 flex-col items-center gap-2">{qr}</div>
          {facts}
        </div>
      ) : (
        facts
      )}
      {notes && (
        <div>
          <h3 className="text-sm font-medium text-fg-1">{labels.notes}</h3>
          <p className="mt-1 whitespace-pre-line text-sm leading-relaxed text-fg-2">{notes}</p>
        </div>
      )}
      {actions && <div className={cn(wide ? "mt-auto flex flex-wrap items-center gap-4" : "flex flex-col gap-1")}>{actions}</div>}
      {steps && steps.length > 0 && (
        <details open={stepsOpen} className="rounded-2 bg-bg-2 px-4 py-3 text-sm">
          <summary className="cursor-pointer font-medium text-fg-1">{labels.help}</summary>
          <ol className="mt-2 list-decimal space-y-1 pl-5 leading-relaxed text-fg-2">
            {steps.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ol>
        </details>
      )}
    </motion.section>
  );
}

function Fact({ label, right, children }: { label: ReactNode; right: boolean; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-3">{label}</dt>
      <dd className={cn("min-w-0 text-fg-1 tabular-nums", right && "text-right")}>{children}</dd>
    </>
  );
}
