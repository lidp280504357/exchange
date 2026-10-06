import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api/errors";
import { useContentMode } from "../platform/hooks";
import { useSettings } from "../settings/store";
import { frontString, parseFrontMatter } from "./frontmatter";
import { useModeKnown } from "./hooks";
import { articleModes, bundledFile, CONTENT_FALLBACK, fetchOne, listedAs, shownIn, type ContentLocale } from "./loader";
import { renderByMode, type ContentMode } from "./markdown";

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

/**
 * loadHero returns the hero in a language and mode, null when the console
 * took it off or there is none. The home section's list says whether the
 * console published one (listedAs): the hero itself is asked for only
 * then, or when the list cannot tell, so a site without one logs no 404
 * (B117).
 */
export async function loadHero(locale: ContentLocale, mode: ContentMode): Promise<Hero | null> {
  const listed = await listedAs("home", "home-hero", locale);
  if (listed === "withdrawn") return null;
  if (listed !== "none") {
    try {
      const a = await fetchOne("home", "home-hero", locale);
      return heroFrom(a.title, a.summary, renderByMode(a.body, mode));
    } catch (err) {
      if (err instanceof ApiError && err.code === "NOTIFY_ARTICLE_WITHDRAWN") return null;
      // Not published (404) or the API out of reach: the bundled draft.
    }
  }
  const src = (await bundledFile("home", "home-hero", locale)) ?? (await bundledFile("home", "home-hero", CONTENT_FALLBACK));
  if (src === null) return null;
  const { data, body } = parseFrontMatter(src);
  if (!shownIn(articleModes(frontString(data, "modes")), mode)) return null;
  return heroFrom(frontString(data, "title"), frontString(data, "summary"), renderByMode(body, mode));
}

/** useHero returns the home page's hero in the user's language and the exchange's mode (null while loading or when there is none). */
export function useHero() {
  const locale = useSettings((s) => s.locale);
  const mode = useContentMode();
  const known = useModeKnown();
  return useQuery({
    queryKey: ["content", "home", locale, mode, "home-hero"],
    queryFn: () => loadHero(locale, mode),
    staleTime: 60_000,
    enabled: known,
  });
}
