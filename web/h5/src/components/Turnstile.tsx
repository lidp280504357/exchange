import { useEffect, useRef } from "react";

// Cloudflare Turnstile (§6.3). The site key is public and comes from the
// build environment; tokens are single use, so callers bump `generation`
// to get a fresh widget after each submission.
const SITE_KEY = import.meta.env.TURNSTILE_SITE_KEY as string | undefined;
const SCRIPT = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

type TurnstileAPI = {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string;
  remove: (id: string) => void;
};

declare global {
  interface Window {
    turnstile?: TurnstileAPI;
    // End-to-end tests (web/h5/e2e) put the environment's captcha bypass
    // token here instead of solving the widget. The server accepts that
    // token only outside production, so this adds nothing a client could
    // not already send to the API.
    __E2E_CAPTCHA_TOKEN__?: string;
  }
}

let loading: Promise<TurnstileAPI> | null = null;

function load(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  loading ??= new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = SCRIPT;
    s.async = true;
    s.onload = () => (window.turnstile ? resolve(window.turnstile) : reject(new Error("turnstile missing")));
    s.onerror = () => reject(new Error("turnstile blocked"));
    document.head.appendChild(s);
  });
  return loading;
}

export function Turnstile({ onToken, generation = 0 }: { onToken: (token: string) => void; generation?: number }) {
  const box = useRef<HTMLDivElement>(null);
  const testToken = window.__E2E_CAPTCHA_TOKEN__;
  useEffect(() => {
    if (testToken) {
      onToken(testToken);
      return;
    }
    if (!SITE_KEY || !box.current) return;
    let id: string | undefined;
    let cancelled = false;
    onToken("");
    load()
      .then((ts) => {
        if (cancelled || !box.current) return;
        id = ts.render(box.current, {
          sitekey: SITE_KEY,
          theme: "dark",
          callback: (t: string) => onToken(t),
          "expired-callback": () => onToken(""),
        });
      })
      .catch(() => onToken(""));
    return () => {
      cancelled = true;
      if (id && window.turnstile) window.turnstile.remove(id);
    };
    // Only a new generation re-renders the widget; onToken changes identity
    // on every render of the parent.
  }, [generation]);
  if (testToken) return null;
  if (!SITE_KEY) return <p className="text-xs text-yellow-300">TURNSTILE_SITE_KEY is not set for this build.</p>;
  return <div ref={box} className="min-h-[65px]" />;
}
