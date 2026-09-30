import { applySettings, createLive, createQueryClient, initI18n, LiveProvider, reportVitals, restoreSession, useSettings } from "@exchange/core";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig } from "motion/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App";
import { pcMessages } from "./i18n";
import "./index.css";

// Start-up: strings, the saved settings on <html>, one WebSocket and market
// store for the app's life, the session from the refresh cookie.
initI18n({ "zh-CN": { ...uiMessages["zh-CN"], ...pcMessages["zh-CN"] }, en: { ...uiMessages.en, ...pcMessages.en } });
applySettings(useSettings.getState());
useSettings.subscribe((s) => applySettings(s));
const queryClient = createQueryClient();
const live = createLive();
void restoreSession();
reportVitals("pc");

const app = (
  <QueryClientProvider client={queryClient}>
    <LiveProvider ws={live.ws} market={live.market}>
      <MotionConfig reducedMotion="user">
        <BrowserRouter>
          <App queryClient={queryClient} />
        </BrowserRouter>
      </MotionConfig>
    </LiveProvider>
  </QueryClientProvider>
);

createRoot(document.getElementById("root")!).render(import.meta.env.DEV ? <StrictMode>{app}</StrictMode> : app);
