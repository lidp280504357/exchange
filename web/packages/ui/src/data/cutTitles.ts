// A table's cells cut their text short with an ellipsis when the window
// is narrow (a turnover at 1024 px, a time, an IP): those cells carry their
// whole text as a title, so it can still be read.

/** Elements inside a cell that cut their own text short. */
const CLIPPING = ".truncate, .text-ellipsis, [class*='line-clamp-']";

/** What a cell shows no one: screen-reader-only labels (a copy button's) and hidden icons. */
const UNSEEN = ".sr-only, [aria-hidden='true'], svg";

/** The title titleCutCells set on a cell: any other title is the column's own. */
const ours = new WeakMap<HTMLElement, string>();

/**
 * seenText is the text an element shows: its innerText without the parts
 * UNSEEN marks, blocks set apart by spaces.
 */
function seenText(el: HTMLElement): string {
  if (!el.querySelector(UNSEEN)) return el.innerText;
  let text = "";
  for (const node of el.childNodes) {
    if (node.nodeType === Node.TEXT_NODE) {
      text += node.textContent ?? "";
    } else if (node instanceof HTMLElement && !node.matches(UNSEEN)) {
      const display = getComputedStyle(node).display;
      if (display === "none") continue;
      text += display.startsWith("inline") ? seenText(node) : ` ${seenText(node)} `;
    }
  }
  return text;
}

/** unseen tells whether el is in a part of the cell that UNSEEN marks. */
function unseen(el: HTMLElement, cell: HTMLElement): boolean {
  for (let e: HTMLElement | null = el; e && e !== cell; e = e.parentElement) {
    if (e.matches(UNSEEN)) return true;
  }
  return false;
}

/**
 * cutText is the text a cell cuts short: the whole cell's when the cell
 * itself does, else that of the elements in it that do (a cut label beside
 * a badge titles the label, not the badge); null when nothing is cut.
 */
function cutText(cell: HTMLElement): string | null {
  if (cell.scrollWidth > cell.clientWidth + 1) return seenText(cell);
  const cut = [...cell.querySelectorAll<HTMLElement>(CLIPPING)].filter(
    (el) => (el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1) && !unseen(el, cell),
  );
  return cut.length > 0 ? cut.map(seenText).join(" ") : null;
}

/**
 * titleCutCells gives each body cell of the table that cuts its text short
 * a title with the whole text, and takes it back once the cell shows all
 * of it; a cell with a title of its own keeps that. It reads every cell
 * before it writes, so the table is laid out once.
 */
export function titleCutCells(table: HTMLTableElement): void {
  const cells = [...table.querySelectorAll<HTMLTableCellElement>(":scope > tbody > tr > td")].filter(
    (cell) => !cell.hasAttribute("title") || cell.title === ours.get(cell),
  );
  const cut = cells.map(cutText);
  cells.forEach((cell, i) => {
    const text = (cut[i] ?? "").replace(/\s+/g, " ").trim();
    if (text) {
      if (cell.title !== text) cell.title = text;
      ours.set(cell, text);
    } else if (ours.has(cell)) {
      cell.removeAttribute("title");
      ours.delete(cell);
    }
  });
}
