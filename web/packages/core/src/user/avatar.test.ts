import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/errors";
import { useSession } from "../session/store";
import { AVATAR_FILE_MAX, avatarCrop, checkAvatarFile, checkUsername, nextUsernameChange, uploadAvatar, USERNAME_COOLDOWN } from "./avatar";
import { avatarOf } from "./profile";

describe("usernames", () => {
  it("take 3 to 20 letters, digits or underscores, not starting with one, nothing reserved", () => {
    expect(checkUsername("satoshi_n")).toBeNull();
    expect(checkUsername("abc")).toBeNull();
    expect(checkUsername("A".repeat(20))).toBeNull();
    expect(checkUsername("Ab1_")).toBeNull();
    expect(checkUsername("ab")).toBe("length");
    expect(checkUsername("a".repeat(21))).toBe("length");
    expect(checkUsername("_abc")).toBe("start");
    expect(checkUsername("ab c")).toBe("chars");
    expect(checkUsername("张三丰")).toBe("chars");
    expect(checkUsername("a-b-c")).toBe("chars");
    // Whole reserved names, and parts no name may contain (any case).
    expect(checkUsername("House")).toBe("reserved");
    expect(checkUsername("bot")).toBe("reserved");
    expect(checkUsername("robot")).toBeNull();
    expect(checkUsername("the_ADMIN_2")).toBe("reserved");
    expect(checkUsername("astrasfan")).toBe("reserved");
  });

  it("may change again 7 days after the last change; a drawn one at once", () => {
    const at = Date.parse("2026-10-07T02:00:00Z");
    expect(nextUsernameChange({ username_changed_at: null }, at)).toBeNull();
    const changed = { username_changed_at: "2026-10-07T01:00:00Z" };
    expect(nextUsernameChange(changed, at)).toBe(Date.parse("2026-10-07T01:00:00Z") + USERNAME_COOLDOWN);
    expect(nextUsernameChange(changed, Date.parse("2026-10-14T01:00:00Z"))).toBeNull();
  });
});

describe("avatars", () => {
  it("show the small picture up to 32 px, the large one above, none for the built-in one", () => {
    const p = { avatar_url: "/uploads/avatars/u/a.webp", avatar_thumb_url: "/uploads/avatars/u/a_64.webp" };
    expect(avatarOf(p, 28)).toBe(p.avatar_thumb_url);
    expect(avatarOf(p, 40)).toBe(p.avatar_url);
    expect(avatarOf({ avatar_url: p.avatar_url, avatar_thumb_url: null }, 24)).toBe(p.avatar_url);
    expect(avatarOf({ avatar_url: null, avatar_thumb_url: null }, 40)).toBeUndefined();
    expect(avatarOf(undefined, 40)).toBeUndefined();
  });

  it("are PNG, JPEG or WebP files the browser can shrink", () => {
    expect(checkAvatarFile({ type: "image/jpeg", size: 3_000_000 })).toBeNull();
    expect(checkAvatarFile({ type: "image/webp", size: AVATAR_FILE_MAX })).toBeNull();
    expect(checkAvatarFile({ type: "image/gif", size: 1000 })).toBe("type");
    expect(checkAvatarFile({ type: "image/svg+xml", size: 1000 })).toBe("type");
    expect(checkAvatarFile({ type: "", size: 1000 })).toBe("type");
    expect(checkAvatarFile({ type: "image/png", size: AVATAR_FILE_MAX + 1 })).toBe("large");
  });

  it("keep the middle square, sent at most 512 px a side", () => {
    expect(avatarCrop(4032, 3024)).toEqual({ sx: 504, sy: 0, side: 3024, out: 512 });
    expect(avatarCrop(300, 500)).toEqual({ sx: 0, sy: 100, side: 300, out: 300 });
    expect(avatarCrop(64, 64)).toEqual({ sx: 0, sy: 0, side: 64, out: 64 });
  });
});

// FakeXHR answers the uploads in turn with the given responses.
function fakeXHR(responses: { status: number; body: unknown }[]) {
  const sent: { url: string; auth: string | null; credentials: boolean; file: FormDataEntryValue | null }[] = [];
  class FakeXHR {
    static sent = sent;
    status = 0;
    statusText = "";
    response: unknown = null;
    responseType = "";
    withCredentials = false;
    upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    private url = "";
    private headers: Record<string, string> = {};
    open(_method: string, url: string) {
      this.url = url;
    }
    setRequestHeader(k: string, v: string) {
      this.headers[k] = v;
    }
    abort() {
      this.onabort?.();
    }
    send(form: FormData) {
      sent.push({ url: this.url, auth: this.headers.Authorization ?? null, credentials: this.withCredentials, file: form.get("file") });
      const r = responses.shift() ?? { status: 500, body: null };
      setTimeout(() => {
        this.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 });
        this.upload.onprogress?.({ lengthComputable: true, loaded: 100, total: 100 });
        this.status = r.status;
        this.response = r.body;
        this.onload?.();
      }, 0);
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
  return sent;
}

const session = { accessToken: "t1", userId: "u1", sessionId: "s1", scope: "", expiresAt: Date.now() + 60_000 };
const profile = { user_id: "u1", username: "user_k3x9q2ab", avatar_url: "/uploads/avatars/u1/a.webp", avatar_thumb_url: "/uploads/avatars/u1/a_64.webp" };

describe("the avatar upload", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    useSession.getState().set(null);
  });

  it("sends the picture with the access token, its progress on the way", async () => {
    useSession.getState().set(session);
    const sent = fakeXHR([{ status: 200, body: profile }]);
    const progress: number[] = [];
    const got = await uploadAvatar(new Blob(["x"], { type: "image/webp" }), (f) => progress.push(f));
    expect(got).toEqual(profile);
    expect(progress).toEqual([0.5, 1]);
    expect(sent).toHaveLength(1);
    expect(sent[0]?.url).toMatch(/\/v1\/user\/avatar$/);
    expect(sent[0]?.auth).toBe("Bearer t1");
    expect(sent[0]?.credentials).toBe(true);
    expect((sent[0]?.file as Blob).type).toBe("image/webp");
  });

  it("refreshes an expired token and sends once more", async () => {
    useSession.getState().set(session);
    const sent = fakeXHR([
      { status: 401, body: { code: "AUTH_TOKEN_EXPIRED", message: "expired" } },
      { status: 200, body: profile },
    ]);
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        new Response(JSON.stringify({ user_id: "u1", session_id: "s1", access_token: "t2", scope: "", expires_at: new Date(Date.now() + 60_000).toISOString() }), {
          status: 200,
        }),
      ),
    );
    await expect(uploadAvatar(new Blob(["x"], { type: "image/png" }))).resolves.toEqual(profile);
    expect(sent.map((s) => s.auth)).toEqual(["Bearer t1", "Bearer t2"]);
    expect((sent[1]?.file as Blob).type).toBe("image/png");
  });

  it("throws the server's error, and a proxy's bare 413 as too large", async () => {
    useSession.getState().set(session);
    fakeXHR([
      { status: 403, body: { code: "USER_FROZEN", message: "frozen" } },
      { status: 413, body: null },
    ]);
    const frozen = await uploadAvatar(new Blob(["x"])).catch((e: unknown) => e);
    expect(frozen).toBeInstanceOf(ApiError);
    expect((frozen as ApiError).code).toBe("USER_FROZEN");
    const large = await uploadAvatar(new Blob(["x"])).catch((e: unknown) => e);
    expect((large as ApiError).code).toBe("USER_AVATAR_TOO_LARGE");
  });
});
