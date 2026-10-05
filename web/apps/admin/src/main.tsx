import { createQueryClient, initI18n, useSettings } from "@exchange/core";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App";
import { entryMessages } from "./i18n";
import { preloadConsole } from "./preload";
import { applyTheme } from "./theme";
import "./index.css";

// The console is light unless switched to dark (design 2026-10-02 §6) and in
// Chinese by default. The pages' strings come with the signed-in console,
// and motion's configuration too (layout/SignedIn: the sign-in page animates
// with CSS alone, so its chunk carries no motion; A40).
initI18n({ "zh-CN": { ...uiMessages["zh-CN"], ...entryMessages["zh-CN"] }, en: { ...uiMessages.en, ...entryMessages.en } });
applyTheme();
useSettings.subscribe(applyTheme);
const queryClient = createQueryClient();
// The shell's and the page's chunks start with the session's check (C6).
preloadConsole(location.pathname);

const app = (
  <QueryClientProvider client={queryClient}>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </QueryClientProvider>
);

createRoot(document.getElementById("root")!).render(import.meta.env.DEV ? <StrictMode>{app}</StrictMode> : app);
