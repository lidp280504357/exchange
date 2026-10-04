import { createQueryClient, initI18n, useSettings } from "@exchange/core";
import { prefersReducedMotion, uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig } from "motion/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App";
import { adminMessages } from "./i18n";
import { preloadConsole } from "./preload";
import { applyTheme } from "./theme";
import "./index.css";

// The console is light unless switched to dark (design 2026-10-02 §6) and in Chinese by default.
initI18n({ "zh-CN": { ...uiMessages["zh-CN"], ...adminMessages["zh-CN"] }, en: { ...uiMessages.en, ...adminMessages.en } });
applyTheme();
useSettings.subscribe(applyTheme);
const queryClient = createQueryClient();
// The shell's and the page's chunks start with the session's check (C6).
preloadConsole(location.pathname);

const app = (
  <QueryClientProvider client={queryClient}>
    <MotionConfig reducedMotion="user" skipAnimations={prefersReducedMotion()}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MotionConfig>
  </QueryClientProvider>
);

createRoot(document.getElementById("root")!).render(import.meta.env.DEV ? <StrictMode>{app}</StrictMode> : app);
