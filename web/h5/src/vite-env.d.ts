/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly TURNSTILE_SITE_KEY?: string;
  // The API origin of the desktop app build (lib/native.ts), e.g. https://astras.vip.
  readonly VITE_API_ORIGIN?: string;
}
