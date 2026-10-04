// A table's cells cut their text short with an ellipsis when the window
// is narrow (a turnover at 1024 px, a time, an IP): those cells carry their
// whole text as a title, so it can still be read.

/** Elements inside a cell that cut their own text short. */
const CLIPPING = ".truncate, .text-ellipsis, [class*='line-clamp-']";

/** The cells whose title titleCutCells set (and may take back). */
const titled = new WeakSet<HTMLElement>();

/** isCut says whether the cell, or an element in it, cuts its text short. */
function isCut(cell: HTMLElement): boolean {
  if (cell.scrollWidth > cell.clientWidth + 1) return true;
  for (const el of cell.querySelectorAll<HTMLElement>(CLIPPING)) {
    if (el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1) return true;
  }
  return false;
}

/**
 * titleCutCells gives each body cell of the table that cuts its text short
 * a title with the whole text, and takes it back once the cell shows all
 * of it. It reads every cell before it writes, so the table is laid out
 * once.
 */
export function titleCutCells(table: HTMLTableElement): void {
  const cells = [...table.querySelectorAll<HTMLTableCellElement>(":scope > tbody > tr > td")];
  const cut = cells.map(isCut);
  cells.forEach((cell, i) => {
    const text = cut[i] ? cell.innerText.replace(/\s+/g, " ").trim() : "";
    if (text) {
      if (cell.title !== text) cell.title = text;
      titled.add(cell);
    } else if (titled.has(cell)) {
      cell.removeAttribute("title");
      titled.delete(cell);
    }
  });
}
