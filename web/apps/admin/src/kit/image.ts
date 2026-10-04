import { FormError } from "./actions";

// A square image chosen for an upload (an asset's logo, the platform's
// logos and icons): checked as the service will check it again (type,
// size, square, least side) and read as base64 with a preview.

export type ImageMime = "image/png" | "image/svg+xml" | "image/webp";

export type ChosenImage = { data: string; mime: ImageMime; preview: string; size: number };

type Translate = (key: string, opts?: Record<string, unknown>) => string;

/**
 * readSquareImage reads file if it is one of mimes, at most maxBytes and
 * square (an SVG has no pixel size to check); minSide is the least side
 * in pixels when given. The messages are the asset profile's.
 */
export async function readSquareImage(
  file: File,
  { mimes, maxBytes, minSide }: { mimes: ImageMime[]; maxBytes: number; minSide?: number },
  t: Translate,
): Promise<ChosenImage> {
  if (!mimes.includes(file.type as ImageMime)) throw new FormError(t("admin.profile.badType"));
  if (file.size > maxBytes) throw new FormError(t("admin.profile.tooLarge"));
  const preview = await new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(new FormError(t("admin.profile.unreadable")));
    r.readAsDataURL(file);
  });
  if (file.type !== "image/svg+xml") {
    const img = new Image();
    img.src = preview;
    await img.decode().catch(() => {
      throw new FormError(t("admin.profile.unreadable"));
    });
    if (img.naturalWidth !== img.naturalHeight) throw new FormError(t("admin.profile.notSquare", { w: img.naturalWidth, h: img.naturalHeight }));
    if (minSide && img.naturalWidth < minSide) throw new FormError(t("admin.platform.tooSmall", { min: minSide, w: img.naturalWidth }));
  }
  return { data: preview.slice(preview.indexOf(",") + 1), mime: file.type as ImageMime, preview, size: file.size };
}
