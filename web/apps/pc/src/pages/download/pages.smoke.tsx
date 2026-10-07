import { createQueryClient, initI18n, qk, type Locale } from "@exchange/core";
import { appsKey, type PlatformApps } from "@exchange/core/platform/apps";
import { DEFAULT_PROFILE } from "@exchange/core/platform/index";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderToString } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeAll, describe, expect, it } from "vitest";
import { withAreas } from "../../i18n";
import downloadMessages from "../../i18n/download";
import Download from "./Download";

// A server render of the App download page (design 2026-10-07, App
// download page §4): an uploaded Android app and an App Store link, each
// a card with its QR code; "no app yet" while none is offered. The
// language comes from navigator.language, set by each language's test file.

const notes = { "zh-CN": "新增价格提醒", en: "Price alerts" };
const offered: PlatformApps = {
  android: {
    mode: "FILE", url: "https://astras.vip/downloads/android/0192a000-0000-7000-8000-000000000001.apk", install_url: null, ios_install: null,
    package: "vip.astras.app", version: "1.2.0", build: "42", min_os: "24", size: 50541363, sha256: "9f".repeat(32), mobileconfig_url: null, notes,
    updated_at: "2026-10-07T03:00:00Z",
  },
  ios: {
    mode: "LINK", url: "https://apps.apple.com/app/id1234567890", install_url: null, ios_install: "APP_STORE", package: null, version: null,
    build: null, min_os: null, size: null, sha256: null, mobileconfig_url: null, notes: { "zh-CN": "", en: "" }, updated_at: "2026-10-07T03:00:00Z",
  },
};

function render(apps: PlatformApps, path = "/download"): string {
  const qc = createQueryClient();
  qc.setQueryData(qk.platform, DEFAULT_PROFILE);
  qc.setQueryData(appsKey, apps);
  return renderToString(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Download />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/** describePages renders the download page in one language (the stores' initial one). */
export function describePages(locale: Locale) {
  const say = (zh: string, tw: string, en: string) => (locale === "en" ? en : locale === "zh-TW" ? tw : zh);
  describe(`the download page in ${locale}`, () => {
    beforeAll(() => {
      const pc = withAreas(downloadMessages);
      initI18n({
        "zh-CN": { ...uiMessages["zh-CN"], ...pc["zh-CN"] },
        "zh-TW": { ...uiMessages["zh-TW"], ...pc["zh-TW"] },
        en: { ...uiMessages.en, ...pc.en },
      });
    });

    it("an uploaded Android app and an App Store link, a card and a QR code each", () => {
      const html = render(offered);
      expect(html).toContain(say("下载 APK", "下載 APK", "Download APK"));
      expect(html).toContain(say("前往 App Store", "前往 App Store", "Open the App Store"));
      expect(html).toContain("48.2 MB");
      expect(html).toContain(say("Android 7.0 及以上", "Android 7.0 及以上", "Android 7.0 or later"));
      expect(html).toContain(offered.android?.url);
      expect(html).toContain(offered.ios?.url);
      // The QR codes, named by their platforms (review GK, F12).
      for (const p of ["Android", "iOS"]) expect(html).toContain(`aria-label="${say(`${p} 下载二维码`, `${p} 下載二維碼`, `${p} download QR code`)}"`);
      expect(html.indexOf('data-testid="app-android"')).toBeLessThan(html.indexOf('data-testid="app-ios"'));
    });

    it("the app a QR code asks for first", () => {
      const html = render(offered, "/download?platform=ios");
      expect(html.indexOf('data-testid="app-ios"')).toBeLessThan(html.indexOf('data-testid="app-android"'));
    });

    it("no app yet", () => {
      const html = render({ android: null, ios: null });
      expect(html).toContain(say("暂未提供 App", "暫未提供 App", "No app yet"));
      expect(html).not.toContain('data-testid="app-');
    });
  });
}
