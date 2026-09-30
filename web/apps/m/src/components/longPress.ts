// A long press on a touch screen (the markets list: hold a row to add it
// to the favourites, design §7.2). A press fires after `delay` unless the
// finger moves further than `slop` (the page is scrolling) or lifts; a
// press that fired swallows the click that may follow, so the row does not
// also open. Timers are injectable, so the machine is tested without a DOM.

export const LONG_PRESS_MS = 450;
export const LONG_PRESS_SLOP = 10;

export type Point = { x: number; y: number };

/** movedBeyond reports whether a finger went further than slop from where it came down. */
export function movedBeyond(from: Point, to: Point, slop: number): boolean {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  return dx * dx + dy * dy > slop * slop;
}

export type Timers = {
  set: (fn: () => void, ms: number) => unknown;
  clear: (id: unknown) => void;
};

const realTimers: Timers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
};

export type LongPressOptions = { delay?: number; slop?: number; timers?: Timers };

/** LongPress follows one press at a time: start, move, end or cancel, then consumeClick. */
export class LongPress {
  private readonly fire: () => void;
  private readonly delay: number;
  private readonly slop: number;
  private readonly timers: Timers;
  private timer: unknown = null;
  private origin: Point | null = null;
  private fired = false;

  constructor(fire: () => void, { delay = LONG_PRESS_MS, slop = LONG_PRESS_SLOP, timers = realTimers }: LongPressOptions = {}) {
    this.fire = fire;
    this.delay = delay;
    this.slop = slop;
    this.timers = timers;
  }

  /** start: a finger (or the main mouse button) went down at p. */
  start(p: Point): void {
    this.cancel();
    this.fired = false;
    this.origin = p;
    this.timer = this.timers.set(() => {
      this.timer = null;
      this.origin = null;
      this.fired = true;
      this.fire();
    }, this.delay);
  }

  /** move: the finger moved; too far and the press is off. */
  move(p: Point): void {
    if (this.origin && movedBeyond(this.origin, p, this.slop)) this.cancel();
  }

  /** end: the finger lifted (a press that already fired still swallows its click). */
  end(): void {
    this.cancel();
  }

  /** cancel: the browser took the gesture (scrolling) or the pointer left. */
  cancel(): void {
    if (this.timer !== null) this.timers.clear(this.timer);
    this.timer = null;
    this.origin = null;
  }

  /** consumeClick reports whether the click after this press must be dropped (once). */
  consumeClick(): boolean {
    const fired = this.fired;
    this.fired = false;
    return fired;
  }

  /** pressing is true while the timer runs. */
  get pressing(): boolean {
    return this.timer !== null;
  }
}
