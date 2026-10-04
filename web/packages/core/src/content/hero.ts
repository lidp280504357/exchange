import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api/errors";
import { useSettings } from "../settings/store";
import { frontString, parseFrontMatter } from "./frontmatter";
import { bundledFile, CONTENT_FALLBACK, fetchOne, type ContentLocale } from "./loader";

// The home page's hero (design 2026-10-04 §4.4, slug home-hero of the HOME
// section): the title, the subtitle (the article's summary) and a button,
// the body's first Markdown link ([text](/path or https URL)). The
// console's version wins over the bundled draft; one it took off leaves
// the sites' own text.

export type Hero = { title: string; subtitle: string; cta: { text: string; href: string } | null };

const LINK = /\[([^\]\n]+)\]\(([^)\s]+)\)/;

/** heroHref keeps a site path or an https URL; anything else is no button. */
export function heroHref(href: string): string | null {
  if (href.startsWith("/") && !href.startsWith("//")) return href;
  return /^https:\/\/[^/\s]+/i.test(href) ? href : null;
}

/** heroFrom reads a hero from its title, subtitle and Markdown body. */
export function heroFrom(title: string, subtitle: string, body: string): Hero {
  const m = LINK.exec(body);
  const href = m ? heroHref(m[2]!) : null;
  return { title: title.trim(), subtitle: subtitle.trim(), cta: m && href ? { text: m[1]!.trim(), href } : null };
}

/** loadHero returns the hero in a language, null when the console took it off or there is none. */
export async function loadHero(locale: ContentLocale): Promise<Hero | null> {
  try {
    const a = await fetchOne("home", "home-hero", locale);
    return heroFrom(a.title, a.summary, a.body);
  } catch (err) {
    if (err instanceof ApiError && err.code === "NOTIFY_ARTICLE_WITHDRAWN") return null;
    // Not published (404) or the API out of reach: the bundled draft.
  }
  const src = (await bundledFile("home", "home-hero", locale)) ?? (await bundledFile("home", "home-hero", CONTENT_FALLBACK));
  if (src === null) return null;
  const { data, body } = parseFrontMatter(src);
  return heroFrom(frontString(data, "title"), frontString(data, "summary"), body);
}

/** useHero returns the home page's hero in the user's language (null while loading or when there is none). */
export function useHero() {
  const locale = useSettings((s) => s.locale);
  return useQuery({ queryKey: ["content", "home", locale, "home-hero"], queryFn: () => loadHero(locale), staleTime: 60_000 });
}
