// A table's cells cut their text short with an ellipsis when the window
// is narrow (a turnover at 1024 px, a time, an IP): those cells carry their
// whole text as a title, so it can still be read.

/** Elements inside a cell that cut their own text short. */
const CLIPPING = ".truncate, .text-ellipsis, [class*='line-clamp-']";

/** The cells whose title titleCutCells set (and may take back). */
const titled = new WeakSet<HTMLElement>();

/**
 * cutText is the text a cell cuts short: the whole cell's when the cell
 * itself does, else that of the elements in it that do (a cut label beside
 * a badge titles the label, not the badge); null when nothing is cut.
 */
function cutText(cell: HTMLElement): string | null {
  if (cell.scrollWidth > cell.clientWidth + 1) return cell.innerText;
  const cut = [...cell.querySelectorAll<HTMLElement>(CLIPPING)].filter(
    (el) => el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1,
  );
  return cut.length > 0 ? cut.map((el) => el.innerText).join(" ") : null;
}

/**
 * titleCutCells gives each body cell of the table that cuts its text short
 * a title with the whole text, and takes it back once the cell shows all
 * of it. It reads every cell before it writes, so the table is laid out
 * once.
 */
export function titleCutCells(table: HTMLTableElement): void {
  const cells = [...table.querySelectorAll<HTMLTableCellElement>(":scope > tbody > tr > td")];
  const cut = cells.map(cutText);
  cells.forEach((cell, i) => {
    const text = (cut[i] ?? "").replace(/\s+/g, " ").trim();
    if (text) {
      if (cell.title !== text) cell.title = text;
      titled.add(cell);
    } else if (titled.has(cell)) {
      cell.removeAttribute("title");
      titled.delete(cell);
    }
  });
}
