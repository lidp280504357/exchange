import { initI18n, setLocale, useSettings } from "@exchange/core";
import type { Preview } from "@storybook/react-vite";
import { uiMessages } from "../src/i18n";
import "../src/styles/index.css";

initI18n(uiMessages);

// Toolbar switches: theme (the user sites are dark, the console light),
// the colour of rises, and the language.
const preview: Preview = {
  globalTypes: {
    theme: {
      description: "Theme",
      toolbar: { icon: "contrast", items: ["dark", "light"], dynamicTitle: true },
    },
    updown: {
      description: "Rise colour",
      toolbar: { icon: "arrowup", items: ["green-up", "red-up"], dynamicTitle: true },
    },
    locale: {
      description: "Language",
      toolbar: { icon: "globe", items: ["zh-CN", "zh-TW", "en"], dynamicTitle: true },
    },
  },
  initialGlobals: { theme: "dark", updown: "green-up", locale: "zh-CN" },
  parameters: {
    layout: "padded",
    backgrounds: { disable: true },
    controls: { expanded: true },
  },
  decorators: [
    (Story, ctx) => {
      const root = document.documentElement;
      root.dataset.theme = ctx.globals.theme ?? "dark";
      root.dataset.updown = ctx.globals.updown ?? "green-up";
      if (useSettings.getState().locale !== ctx.globals.locale) setLocale(ctx.globals.locale ?? "zh-CN");
      return (
        <div className="min-h-[120px] bg-bg-0 p-4 text-fg-1">
          <Story />
        </div>
      );
    },
  ],
};

export default preview;
