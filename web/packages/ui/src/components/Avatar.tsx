import { useState } from "react";
import { cn } from "../lib/cn";
import { identityClass } from "../lib/identity";
import { DefaultAvatar } from "./DefaultAvatar";

export type AvatarProps = {
  /** A name, nickname or email: the initials and the colour come from it. */
  name?: string;
  src?: string;
  /**
   * A user ID: without a picture, or when it fails to load, the user's
   * built-in avatar (one of twelve, chosen by the ID) stands in for the
   * initials (design 2026-10-07, avatars and usernames §1 #4).
   */
  seed?: string;
  /** Diameter in px (default 32). */
  size?: number;
  className?: string;
};

/**
 * initialsOf returns up to two initials: "Ada Lovelace" → "AL",
 * "alice@example.com" → "A", "张三" → "张".
 */
export function initialsOf(name: string | undefined): string {
  const s = (name ?? "").trim().replace(/@.*$/, "");
  if (!s) return "?";
  const words = s.split(/[\s._-]+/).filter(Boolean);
  const first = [...(words[0] ?? s)][0] ?? "?";
  if (/[㐀-鿿]/.test(first)) return first;
  const second = words.length > 1 ? ([...(words[1] ?? "")][0] ?? "") : "";
  return (first + second).toUpperCase();
}

/**
 * Avatar shows a user's picture (a quiet circle while it loads); without
 * one, or once it failed to load, the built-in avatar of the `seed`, or
 * initials on a colour stable per name. A plain image and a fallback: it
 * sits in the PC top bar, so it carries no primitive library.
 */
export function Avatar({ name, src, seed, size = 32, className }: AvatarProps) {
  const [failed, setFailed] = useState<string>();
  const picture = src && src !== failed ? src : undefined;
  return (
    <span
      className={cn(
        "relative inline-flex shrink-0 select-none items-center justify-center overflow-hidden rounded-full align-middle",
        picture && "bg-bg-3",
        className,
      )}
      style={{ width: size, height: size }}
    >
      {picture ? (
        <img src={picture} alt={name ?? ""} draggable={false} onError={() => setFailed(picture)} className="size-full object-cover" />
      ) : seed ? (
        <DefaultAvatar seed={seed} label={name} />
      ) : (
        <span
          role={name ? "img" : undefined}
          aria-label={name}
          className={cn("flex size-full items-center justify-center font-semibold leading-none text-white", identityClass(name ?? "?"))}
          style={{ fontSize: Math.max(10, Math.round(size * 0.4)) }}
        >
          {initialsOf(name)}
        </span>
      )}
    </span>
  );
}
