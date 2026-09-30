import { applySettings, createQueryClient, initI18n, useSettings } from "@exchange/core";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig } from "motion/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App";
import { adminMessages } from "./i18n";
import "./index.css";

// The console is light (design §3 #10) and in Chinese by default.
initI18n({ "zh-CN": { ...uiMessages["zh-CN"], ...adminMessages["zh-CN"] }, en: { ...uiMessages.en, ...adminMessages.en } });
applySettings(useSettings.getState(), "light");
useSettings.subscribe((s) => applySettings(s, "light"));
const queryClient = createQueryClient();

const app = (
  <QueryClientProvider client={queryClient}>
    <MotionConfig reducedMotion="user">
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MotionConfig>
  </QueryClientProvider>
);

createRoot(document.getElementById("root")!).render(import.meta.env.DEV ? <StrictMode>{app}</StrictMode> : app);
