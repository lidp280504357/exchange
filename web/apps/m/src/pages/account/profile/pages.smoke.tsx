import { createQueryClient, initI18n, LiveProvider, MarketStore, qk, useSession, type Locale, type WsClient } from "@exchange/core";
import { DEFAULT_PROFILE } from "@exchange/core/platform/index";
import type { Profile as ProfileData } from "@exchange/core/user/profile";
import { uiMessages } from "@exchange/ui";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderToString } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeAll, describe, expect, it } from "vitest";
import { withAreas } from "../../../i18n";
import accountMessages from "../../../i18n/account";
import profileMessages from "../../../i18n/profile";
import Profile from "../Profile";

// A server render of the phone's profile page (design 2026-10-07, avatars and
// usernames): a username drawn at sign-up with the built-in avatar of the
// user ID, one changed an hour ago (7 days to wait, its row off), and a
// frozen account. The language comes from navigator.language,
// set by each language's test file.

const USER = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e";

function profile(over: Partial<ProfileData> = {}): ProfileData {
  return {
    user_id: USER, username: "user_k3x9q2ab", username_changed_at: null, avatar_url: null, avatar_thumb_url: null, status: "ACTIVE", region: "SG",
    language: "zh-CN", timezone: "Asia/Singapore", anti_phishing_code: "", kyc_level: 0, version: 1, created_at: "2026-10-07T02:54:00Z", ...over,
  } as ProfileData;
}

function render(p: ProfileData): string {
  const qc = createQueryClient();
  qc.setQueryData(qk.platform, DEFAULT_PROFILE);
  qc.setQueryData(qk.profile, p);
  const ws = { subscribe: () => () => {}, onStatus: () => () => {} } as unknown as WsClient;
  return renderToString(
    <QueryClientProvider client={qc}>
      <LiveProvider ws={ws} market={new MarketStore(ws, (cb) => cb())}>
        <MemoryRouter initialEntries={["/account/profile"]}>
          <Profile />
        </MemoryRouter>
      </LiveProvider>
    </QueryClientProvider>,
  );
}

/** describePages renders the profile page in one language (the stores' initial one). */
export function describePages(locale: Locale) {
  const say = (zh: string, tw: string, en: string) => (locale === "en" ? en : locale === "zh-TW" ? tw : zh);
  describe(`the profile page in ${locale}`, () => {
    beforeAll(() => {
      const m = withAreas(accountMessages, profileMessages);
      initI18n({
        "zh-CN": { ...uiMessages["zh-CN"], ...m["zh-CN"] },
        "zh-TW": { ...uiMessages["zh-TW"], ...m["zh-TW"] },
        en: { ...uiMessages.en, ...m.en },
      });
      useSession.getState().set({ accessToken: "t", userId: USER, sessionId: "s", scope: "", expiresAt: Date.now() + 60_000 });
    });

    it("a username drawn at sign-up, and the built-in avatar of the user ID", () => {
      const html = render(profile());
      expect(html).toContain(say("用户名", "使用者名稱", "Username"));
      expect(html).toContain("user_k3x9q2ab");
      expect(html).toContain(say("更换头像", "更換頭像", "Change avatar"));
      // The UID shortened, the whole one to copy.
      expect(html).toContain("0192f0c4…1c2e");
      expect(html).toContain(USER);
      // Index 0 for this ID (ui's Avatar test pins it).
      expect(html).toContain('data-avatar-default="0"');
      expect(html).not.toContain('disabled=""');
    });

    it("a username changed an hour ago waits 7 days", () => {
      const html = render(profile({ username: "satoshi_n", username_changed_at: new Date(Date.now() - 3600_000).toISOString() }));
      expect(html).toContain("satoshi_n");
      expect(html).toContain(say("后可再次修改", "後可再次修改", "May change again after"));
      expect(html).toContain('disabled=""');
    });

    it("a frozen account sees its profile without the changes", () => {
      const html = render(profile({ status: "FROZEN" }));
      expect(html).toContain(say("账户已冻结", "帳戶已凍結", "The account is frozen"));
    });
  });
}
