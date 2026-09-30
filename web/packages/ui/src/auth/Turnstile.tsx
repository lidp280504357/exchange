import { e2eCaptchaToken, loadTurnstile, turnstileSiteKey } from "@exchange/core/auth/turnstile";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type TurnstileProps = {
  /** The token, or "" while there is none (expired, failed, re-rendered). */
  onToken: (token: string) => void;
  /** Bump after each submission: a token works once, a new widget gets the next. */
  generation?: number;
  className?: string;
};

/**
 * Turnstile is Cloudflare's human check (requirements §6.3), in the page's
 * theme. End-to-end tests skip it with their bypass token; a build without
 * a site key says so instead of rendering nothing.
 */
export function Turnstile({ onToken, generation = 0, className }: TurnstileProps) {
  const { t } = useTranslation();
  const box = useRef<HTMLDivElement>(null);
  const bypass = e2eCaptchaToken();
  const siteKey = turnstileSiteKey();
  const latest = useRef(onToken);
  latest.current = onToken;

  useEffect(() => {
    if (bypass) {
      latest.current(bypass);
      return;
    }
    if (!siteKey || !box.current) return;
    let id: string | undefined;
    let cancelled = false;
    latest.current("");
    loadTurnstile()
      .then((ts) => {
        if (cancelled || !box.current) return;
        id = ts.render(box.current, {
          sitekey: siteKey,
          theme: document.documentElement.dataset.theme === "light" ? "light" : "dark",
          callback: (token: string) => latest.current(token),
          "expired-callback": () => latest.current(""),
          "error-callback": () => latest.current(""),
        });
      })
      .catch(() => latest.current(""));
    return () => {
      cancelled = true;
      if (id && window.turnstile) window.turnstile.remove(id);
    };
  }, [generation, bypass, siteKey]);

  if (bypass) return null;
  if (!siteKey) return <p className={cn("text-xs text-warn", className)}>{t("ui.captcha.noKey")}</p>;
  return <div ref={box} className={cn("min-h-[65px]", className)} />;
}
