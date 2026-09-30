/**
 * copyText puts text on the clipboard: the async Clipboard API when the
 * page may use it, else a hidden textarea and execCommand (older WebViews).
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    // Insecure origins and old WebViews have no navigator.clipboard.
    const clipboard = globalThis.navigator?.clipboard as Clipboard | undefined;
    if (clipboard && typeof clipboard.writeText === "function") {
      await clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the textarea
  }
  const doc = globalThis.document;
  if (!doc) return false;
  const area = doc.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  doc.body.appendChild(area);
  area.select();
  let ok = false;
  try {
    ok = doc.execCommand("copy");
  } catch {
    ok = false;
  }
  area.remove();
  return ok;
}
