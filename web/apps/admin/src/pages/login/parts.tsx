import { BellRing, ScrollText, ShieldCheck } from "lucide-react";
import type { InputHTMLAttributes } from "react";
import { useTranslation } from "react-i18next";
import { Wordmark } from "../../layout/Brand";

// The sign-in's parts, shared with the one-time setup page (C5.5 ⑪).

/** Field is a labelled input whose underline grows from the middle when it has the focus. */
export function Field({ label, ...input }: { label: string } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="group mb-4 block">
      <span className="text-sm text-fg-2">{label}</span>
      <span className="relative mt-1.5 block">
        <input
          {...input}
          className="h-11 w-full rounded-2 border border-line-2 bg-bg-1 px-3 text-base text-fg-1 outline-none transition-colors placeholder:text-fg-3 hover:border-fg-3 focus-visible:outline-none"
        />
        <span
          aria-hidden
          className="pointer-events-none absolute inset-x-1.5 bottom-0 h-0.5 origin-center scale-x-0 rounded-full bg-brand transition-transform duration-[var(--t-base)] ease-out group-focus-within:scale-x-100"
        />
      </span>
    </label>
  );
}

/** BrandPanel is the left half: a grid fading out, two slow lights, the wordmark drawn. */
export function BrandPanel() {
  const { t } = useTranslation();
  return (
    <aside data-theme="dark" className="relative hidden overflow-hidden bg-bg-0 text-fg-1 lg:flex lg:flex-col lg:justify-between lg:p-12">
      <div aria-hidden className="login-grid absolute inset-0" />
      <div aria-hidden className="absolute -left-32 top-[12%] size-[30rem] rounded-full bg-brand opacity-20 blur-[100px] animate-drift-a will-change-transform" />
      <div aria-hidden className="absolute -right-24 bottom-[-10%] size-[26rem] rounded-full bg-info opacity-20 blur-[110px] animate-drift-b will-change-transform" />
      <Wordmark animated className="relative h-11 self-start" />
      <div className="relative max-w-md">
        <h1 className="text-2xl font-semibold leading-snug">{t("admin.brandTitle")}</h1>
        <p className="mt-3 text-sm leading-relaxed text-fg-2">{t("admin.brandText")}</p>
        <ul className="mt-8 flex flex-col gap-3 text-sm text-fg-2">
          {[
            { icon: ShieldCheck, key: "brandApprovals" },
            { icon: BellRing, key: "brandLive" },
            { icon: ScrollText, key: "brandAudit" },
          ].map((f, i) => (
            <li key={f.key} className="flex items-center gap-3 animate-[rise_480ms_var(--ease)_backwards]" style={{ animationDelay: `${600 + i * 120}ms` }}>
              <span className="grid size-8 place-items-center rounded-2 bg-brand-soft text-brand">
                <f.icon size={16} />
              </span>
              {t(`admin.${f.key}`)}
            </li>
          ))}
        </ul>
      </div>
      <div className="relative text-xs text-fg-3">{t("admin.brandFoot")}</div>
    </aside>
  );
}
