import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { refresh, unwrap, userApi } from "../api/client";
import { ApiError, errorFrom } from "../api/errors";
import { useSession } from "../session/store";
import { keepProfile, type Profile } from "./profile";

// The username and the avatar (design 2026-10-07, avatars and usernames;
// api/openapi/user.yaml): a username's rules as the server keeps them
// (internal/user/domain/username.go; the server decides, these only say so
// before asking), when it may change again, and the avatar's upload, shrunk
// in the browser first. Only the profile pages load this; what every page
// shows (avatarOf) is in ./profile.

export const USERNAME_MIN = 3;
export const USERNAME_MAX = 20;
/** How long a changed username stays (the server's UsernameCooldown). */
export const USERNAME_COOLDOWN = 7 * 24 * 3600_000;

// Names nobody may take, and parts no name may contain: they would pass for
// the platform or its staff.
const RESERVED_WHOLE = new Set([
  "house", "root", "system", "null", "undefined", "api", "www", "help", "service", "security", "platform", "exchange", "operator", "bot",
]);
const RESERVED_PART = ["admin", "astras", "support", "official", "staff", "moderator"];

export type UsernameProblem = "chars" | "start" | "length" | "reserved";

/**
 * checkUsername mirrors the contract: 3 to 20 letters, digits or
 * underscores, not starting with an underscore, not a reserved name.
 */
export function checkUsername(name: string): UsernameProblem | null {
  if (!/^[A-Za-z0-9_]*$/.test(name)) return "chars";
  if (name.startsWith("_")) return "start";
  if (name.length < USERNAME_MIN || name.length > USERNAME_MAX) return "length";
  const lower = name.toLowerCase();
  if (RESERVED_WHOLE.has(lower) || RESERVED_PART.some((part) => lower.includes(part))) return "reserved";
  return null;
}

/**
 * nextUsernameChange is when the username may change again (ms since the
 * epoch); null when it may now: never changed (drawn at sign-up, or by an
 * operator's reset), or the 7 days are over.
 */
export function nextUsernameChange(p: Pick<Profile, "username_changed_at">, now: number): number | null {
  if (!p.username_changed_at) return null;
  const next = Date.parse(p.username_changed_at) + USERNAME_COOLDOWN;
  return Number.isFinite(next) && next > now ? next : null;
}

/** changeUsername renames the caller (USER_USERNAME_INVALID, USER_USERNAME_TAKEN, USER_USERNAME_COOLDOWN). */
export function changeUsername(username: string): Promise<Profile> {
  return unwrap(userApi.PUT("/v1/user/username", { body: { username } }));
}

/** deleteAvatar goes back to the built-in avatar. */
export function deleteAvatar(): Promise<Profile> {
  return unwrap(userApi.DELETE("/v1/user/avatar"));
}

/** The pictures the server takes (it reads their content). */
export const AVATAR_TYPES = ["image/png", "image/jpeg", "image/webp"] as const;
/** The largest file the browser opens to shrink: a phone's photo is well under. */
export const AVATAR_FILE_MAX = 20 * 1024 * 1024;
/** The server's shortest side. */
export const AVATAR_MIN_SIDE = 64;
/** The side of what is sent: the server keeps it at 256 and 64. */
export const AVATAR_SEND_SIDE = 512;

/** Why a picture is refused before it is sent. */
export type AvatarProblem = "type" | "large" | "small" | "decode";

export class AvatarProblemError extends Error {
  constructor(public problem: AvatarProblem) {
    super(`avatar: ${problem}`);
    this.name = "AvatarProblemError";
  }
}

/** checkAvatarFile refuses what cannot be an avatar before opening it. */
export function checkAvatarFile(file: Pick<File, "type" | "size">): AvatarProblem | null {
  if (!(AVATAR_TYPES as readonly string[]).includes(file.type)) return "type";
  return file.size > AVATAR_FILE_MAX ? "large" : null;
}

/** avatarCrop is a picture's middle square (the server keeps the same) and the side it is drawn at. */
export function avatarCrop(width: number, height: number, max = AVATAR_SEND_SIDE): { sx: number; sy: number; side: number; out: number } {
  const side = Math.min(width, height);
  return { sx: Math.floor((width - side) / 2), sy: Math.floor((height - side) / 2), side, out: Math.min(side, max) };
}

/**
 * prepareAvatar opens a picture in the browser and draws its middle square
 * at most 512 px a side, as WebP (PNG where the browser cannot write WebP):
 * what is sent stays far within the server's limits (5 MB, 2048 px a side,
 * 8 bits a channel; review C52), turned upright by its EXIF orientation,
 * without its metadata.
 */
export async function prepareAvatar(file: File): Promise<Blob> {
  const problem = checkAvatarFile(file);
  if (problem) throw new AvatarProblemError(problem);
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    try {
      await img.decode();
    } catch {
      throw new AvatarProblemError("decode");
    }
    const { naturalWidth: w, naturalHeight: h } = img;
    if (!w || !h) throw new AvatarProblemError("decode");
    if (Math.min(w, h) < AVATAR_MIN_SIDE) throw new AvatarProblemError("small");
    const { sx, sy, side, out } = avatarCrop(w, h);
    const canvas = document.createElement("canvas");
    canvas.width = out;
    canvas.height = out;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new AvatarProblemError("decode");
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(img, sx, sy, side, side, 0, 0, out, out);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/webp", 0.9));
    if (!blob) throw new AvatarProblemError("decode");
    return blob;
  } finally {
    URL.revokeObjectURL(url);
  }
}

type Sent = { status: number; statusText: string; body: unknown };

const origin = globalThis.location?.origin ?? "http://localhost";

// post sends the picture with XMLHttpRequest, which (unlike fetch) reports
// the upload's progress.
function post(form: FormData, token: string | undefined, onProgress: (fraction: number) => void, signal?: AbortSignal): Promise<Sent> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("aborted", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${origin}/v1/user/avatar`);
    xhr.withCredentials = true;
    xhr.responseType = "json";
    if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => resolve({ status: xhr.status, statusText: xhr.statusText, body: xhr.response as unknown });
    xhr.onerror = () => reject(new ApiError(0, "COMMON_UNAVAILABLE", "network error"));
    xhr.onabort = () => reject(new DOMException("aborted", "AbortError"));
    signal?.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(form);
  });
}

/**
 * uploadAvatar sends a prepared picture and returns the profile with the
 * new avatar. An expired access token is refreshed and the upload sent
 * once more, as every other request (api/client's authFetch).
 */
export async function uploadAvatar(picture: Blob, onProgress: (fraction: number) => void = () => {}, signal?: AbortSignal): Promise<Profile> {
  const form = new FormData();
  form.append("file", picture, picture.type === "image/webp" ? "avatar.webp" : "avatar.png");
  let sent = await post(form, useSession.getState().session?.accessToken, onProgress, signal);
  if (sent.status === 401) {
    const err = errorFrom(401, sent.statusText, sent.body);
    if (err.code === "AUTH_TOKEN_EXPIRED") {
      const s = await refresh();
      if (s) sent = await post(form, s.accessToken, onProgress, signal);
    } else if (err.code === "AUTH_SESSION_REVOKED") {
      useSession.getState().set(null);
    }
  }
  if (sent.status < 200 || sent.status >= 300) {
    // A proxy's 413 page carries no code.
    throw errorFrom(sent.status, sent.statusText, sent.body ?? (sent.status === 413 ? { code: "USER_AVATAR_TOO_LARGE" } : null));
  }
  return sent.body as Profile;
}

// preload puts pictures in the browser's cache (or gives up after ms), so
// the avatars switching to a new one do not show the built-in one while it
// loads.
function preload(urls: string[], ms = 4000): Promise<void> {
  const each = urls.map(
    (u) =>
      new Promise<void>((resolve) => {
        const img = new Image();
        img.onload = () => resolve();
        img.onerror = () => resolve();
        img.src = u;
      }),
  );
  return Promise.race([Promise.all(each).then(() => undefined), new Promise<void>((resolve) => setTimeout(resolve, ms))]);
}

/** The avatar upload's state: `preview` is the picture as sent; progress 1 means the server is at it. */
export type AvatarUpload =
  | { phase: "idle" }
  | { phase: "preparing" }
  | { phase: "uploading"; preview: string; progress: number }
  | { phase: "failed"; error: unknown };

/**
 * useAvatarUpload uploads a chosen picture: shrunk in the browser, sent
 * with its progress, the new avatar fetched before the cached profile (and
 * so every avatar of the user on the page) switches to it. `upload`
 * returns the new profile, or null when it failed or was cancelled.
 */
export function useAvatarUpload(): {
  state: AvatarUpload;
  upload: (file: File) => Promise<Profile | null>;
  cancel: () => void;
  dismiss: () => void;
} {
  const qc = useQueryClient();
  const [state, setState] = useState<AvatarUpload>({ phase: "idle" });
  const current = useRef<AbortController | null>(null);
  const preview = useRef<string | null>(null);

  const release = useCallback(() => {
    if (preview.current) URL.revokeObjectURL(preview.current);
    preview.current = null;
  }, []);

  useEffect(
    () => () => {
      current.current?.abort();
      current.current = null;
      release();
    },
    [release],
  );

  const upload = useCallback(
    async (file: File) => {
      current.current?.abort();
      release();
      const ctl = new AbortController();
      current.current = ctl;
      // A cancelled or replaced upload says nothing more.
      const set = (s: AvatarUpload) => {
        if (current.current === ctl) setState(s);
      };
      set({ phase: "preparing" });
      try {
        const picture = await prepareAvatar(file);
        if (current.current !== ctl) return null;
        const url = URL.createObjectURL(picture);
        preview.current = url;
        set({ phase: "uploading", preview: url, progress: 0 });
        const profile = await uploadAvatar(picture, (progress) => set({ phase: "uploading", preview: url, progress }), ctl.signal);
        set({ phase: "uploading", preview: url, progress: 1 });
        const urls = [profile.avatar_url, profile.avatar_thumb_url].filter((u): u is string => Boolean(u));
        await preload(urls);
        if (current.current !== ctl) return null;
        keepProfile(qc, profile);
        set({ phase: "idle" });
        current.current = null;
        release();
        return profile;
      } catch (e) {
        if (current.current === ctl) {
          set({ phase: "failed", error: e });
          current.current = null;
          release();
        }
        return null;
      }
    },
    [qc, release],
  );

  const cancel = useCallback(() => {
    current.current?.abort();
    current.current = null;
    release();
    setState({ phase: "idle" });
  }, [release]);

  // dismiss clears a failure's message (an upload going on stays).
  const dismiss = useCallback(() => setState((s) => (s.phase === "failed" ? { phase: "idle" } : s)), []);

  return { state, upload, cancel, dismiss };
}
