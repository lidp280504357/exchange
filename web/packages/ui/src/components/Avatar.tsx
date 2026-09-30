import { Avatar as RAvatar } from "radix-ui";
import { cn } from "../lib/cn";
import { identityClass } from "../lib/identity";

export type AvatarProps = {
  /** A name, nickname or email: the initials and the colour come from it. */
  name?: string;
  src?: string;
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

/** Avatar shows a user's picture, or initials on a colour stable per name. */
export function Avatar({ name, src, size = 32, className }: AvatarProps) {
  return (
    <RAvatar.Root
      className={cn("relative inline-flex shrink-0 select-none items-center justify-center overflow-hidden rounded-full align-middle", className)}
      style={{ width: size, height: size }}
    >
      {src && <RAvatar.Image src={src} alt={name ?? ""} className="size-full object-cover" />}
      <RAvatar.Fallback
        delayMs={src ? 250 : undefined}
        aria-label={name}
        className={cn("flex size-full items-center justify-center font-semibold leading-none text-white", identityClass(name ?? "?"))}
        style={{ fontSize: Math.max(10, Math.round(size * 0.4)) }}
      >
        {initialsOf(name)}
      </RAvatar.Fallback>
    </RAvatar.Root>
  );
}
