import { i18n, initI18n } from "@exchange/core";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { uiMessages } from "../i18n";

// Test setup, imported first by each test file (vitest runs without
// globals, so Testing Library cannot register its cleanup itself): the
// shared and ui strings in English, and an unmounted DOM after each test.

initI18n(uiMessages);
void i18n.changeLanguage("en");

afterEach(() => {
  cleanup();
});
