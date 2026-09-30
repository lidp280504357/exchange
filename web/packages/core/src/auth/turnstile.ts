// Cloudflare Turnstile, the human check before a code is sent or after
// failed sign-ins (requirements §6.3). The site key is public and comes
// from the build environment (TURNSTILE_SITE_KEY); the script loads on
// first use; a token works once, so a widget is re-rendered for the next
// attempt.

export type TurnstileAPI = {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string;
  remove: (id: string) => void;
};

declare global {
  interface Window {
    turnstile?: TurnstileAPI;
    // End-to-end tests put the environment's captcha bypass token here
    // instead of solving the widget. The server accepts that token only
    // outside production, so this adds nothing a client could not send.
    __E2E_CAPTCHA_TOKEN__?: string;
  }
}

const SCRIPT = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

let loading: Promise<TurnstileAPI> | null = null;

/** loadTurnstile loads the widget script once. */
export function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  loading ??= new Promise<TurnstileAPI>((resolve, reject) => {
    const s = document.createElement("script");
    s.src = SCRIPT;
    s.async = true;
    s.onload = () => (window.turnstile ? resolve(window.turnstile) : reject(new Error("turnstile missing")));
    s.onerror = () => {
      loading = null;
      reject(new Error("turnstile blocked"));
    };
    document.head.appendChild(s);
  });
  return loading;
}

/** turnstileSiteKey is the public site key, if the build has one. */
export function turnstileSiteKey(): string | undefined {
  return (import.meta.env?.TURNSTILE_SITE_KEY as string | undefined) || undefined;
}

/** e2eCaptchaToken is the bypass token end-to-end tests set, if any. */
export function e2eCaptchaToken(): string | undefined {
  return globalThis.window?.__E2E_CAPTCHA_TOKEN__;
}
