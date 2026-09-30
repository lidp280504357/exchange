import { isHighFrequency, isPrivateChannel, type MarketPush, type PrivatePush, type WsReply } from "./types";

// WsClient is the app's single connection to /v1/ws (design §4.3):
//
// - public and private channels share it; after sign-in it authenticates
//   on the same connection and the private channels follow;
// - subscriptions are reference counted: the last listener leaving starts
//   a 15-second timer before the unsubscribe, so moving between pages
//   neither reconnects nor refetches snapshots;
// - a dropped connection comes back after 1 s doubling to 30 s, ±30%
//   jitter, and replays every subscription (private ones with last_seq);
// - depth updates must follow their prev_seq: a gap asks once for a fresh
//   snapshot and drops updates until it arrives ("syncing");
// - while the page is hidden, high-frequency channels keep only their last
//   message; depth asks for a new snapshot when the page shows again.

export type WsStatus = "idle" | "connecting" | "open" | "reconnecting";

type Listener = (msg: MarketPush | PrivatePush) => void;

/** SocketLike is what the client needs of a WebSocket (tests pass fakes). */
export type SocketLike = {
  readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
};

export type WsOptions = {
  url?: string;
  /** Opens a socket; defaults to the browser's WebSocket. */
  connect?: (url: string) => SocketLike;
  /** The current access token, if signed in. */
  token?: () => string | undefined;
  /** Refreshes the access token (AUTH_TOKEN_EXPIRED); resolves the new one. */
  refresh?: () => Promise<string | undefined>;
  unsubscribeDelay?: number;
  random?: () => number;
  /** Reports whether the page is visible; defaults to document.visibilityState. */
  visible?: () => boolean;
};

const OPEN = 1;
const MAX_SUBSCRIPTIONS = 50;

type Sub = {
  listeners: Set<Listener>;
  /** Pending unsubscribe after the last listener left. */
  timer?: ReturnType<typeof setTimeout>;
  /** Sent to the server (a private channel waits for auth). */
  active: boolean;
  /** Depth: the seq of the last message applied; syncing after a gap. */
  seq?: number;
  syncing?: boolean;
  /** The last message held while the page is hidden. */
  held?: MarketPush | PrivatePush;
};

export class WsClient {
  private readonly opts: Required<Omit<WsOptions, "token" | "refresh">> & Pick<WsOptions, "token" | "refresh">;
  private socket: SocketLike | null = null;
  private subs = new Map<string, Sub>();
  private statusListeners = new Set<(s: WsStatus) => void>();
  private syncListeners = new Set<(channel: string, syncing: boolean) => void>();
  private resyncListeners = new Set<(channels: string[]) => void>();
  private _status: WsStatus = "idle";
  private attempt = 0;
  private retryTimer?: ReturnType<typeof setTimeout>;
  /** The first private subscription has no sequence to replay from: reload once it is up. */
  private catchUp = false;
  private authed = false;
  private authedToken = "";
  private lastSeq = 0;
  private hidden = false;
  private closed = false;

  constructor(opts: WsOptions = {}) {
    const loc = globalThis.location;
    const url = opts.url ?? (loc ? `${loc.protocol === "https:" ? "wss" : "ws"}://${loc.host}/v1/ws` : "ws://localhost/v1/ws");
    this.opts = {
      url,
      connect: opts.connect ?? ((u) => new WebSocket(u) as unknown as SocketLike),
      token: opts.token,
      refresh: opts.refresh,
      unsubscribeDelay: opts.unsubscribeDelay ?? 15_000,
      random: opts.random ?? Math.random,
      visible: opts.visible ?? (() => globalThis.document?.visibilityState !== "hidden"),
    };
    this.hidden = !this.opts.visible();
    globalThis.document?.addEventListener?.("visibilitychange", () => this.setVisible(this.opts.visible()));
  }

  get status(): WsStatus {
    return this._status;
  }

  /** onStatus follows the connection state; returns the unsubscribe. */
  onStatus(fn: (s: WsStatus) => void): () => void {
    this.statusListeners.add(fn);
    return () => this.statusListeners.delete(fn);
  }

  /** onSync follows channels entering and leaving "syncing". */
  onSync(fn: (channel: string, syncing: boolean) => void): () => void {
    this.syncListeners.add(fn);
    return () => this.syncListeners.delete(fn);
  }

  /**
   * onResync is told when private events were lost (the server no longer
   * buffers them): the private data must reload over REST.
   */
  onResync(fn: (channels: string[]) => void): () => void {
    this.resyncListeners.add(fn);
    return () => this.resyncListeners.delete(fn);
  }

  isSyncing(channel: string): boolean {
    return this.subs.get(channel)?.syncing ?? false;
  }

  /** subscribe adds a listener to a channel; returns the unsubscribe. */
  subscribe(channel: string, fn: Listener): () => void {
    let sub = this.subs.get(channel);
    if (!sub) {
      sub = { listeners: new Set(), active: false };
      this.subs.set(channel, sub);
      if (this.subs.size > MAX_SUBSCRIPTIONS && import.meta.env?.DEV) {
        console.warn(`WsClient: ${this.subs.size} channels, the gateway allows ${MAX_SUBSCRIPTIONS}`);
      }
    }
    if (sub.timer) {
      clearTimeout(sub.timer);
      sub.timer = undefined;
    }
    sub.listeners.add(fn);
    this.ensureOpen();
    this.activate(channel, sub);
    let done = false;
    return () => {
      if (done) return;
      done = true;
      this.release(channel, fn);
    };
  }

  /** authenticate signs the connection in (or out, with undefined). */
  authenticate(token: string | undefined): void {
    if (!token) {
      if (!this.authed && !this.authedToken) return;
      // A connection stays with one user: start again anonymously.
      this.authed = false;
      this.authedToken = "";
      this.lastSeq = 0;
      for (const [ch, sub] of this.subs) if (isPrivateChannel(ch)) sub.active = false;
      this.restart();
      return;
    }
    if (token === this.authedToken && this.authed) return;
    this.authedToken = token;
    this.sendAuth();
  }

  /** close ends the connection for good (tests, page unload). */
  close(): void {
    this.closed = true;
    clearTimeout(this.retryTimer);
    for (const sub of this.subs.values()) clearTimeout(sub.timer);
    this.socket?.close(1000, "client closed");
    this.socket = null;
    this.setStatus("idle");
  }

  private release(channel: string, fn: Listener) {
    const sub = this.subs.get(channel);
    if (!sub) return;
    sub.listeners.delete(fn);
    if (sub.listeners.size > 0) return;
    sub.timer = setTimeout(() => {
      if (sub.listeners.size > 0) return;
      this.subs.delete(channel);
      if (sub.active) this.send({ op: "unsubscribe", args: [channel] });
    }, this.opts.unsubscribeDelay);
  }

  private setStatus(s: WsStatus) {
    if (this._status === s) return;
    this._status = s;
    for (const fn of this.statusListeners) fn(s);
  }

  private setSyncing(channel: string, sub: Sub, syncing: boolean) {
    if ((sub.syncing ?? false) === syncing) return;
    sub.syncing = syncing;
    for (const fn of this.syncListeners) fn(channel, syncing);
  }

  private ensureOpen() {
    if (this.closed || this.socket) return;
    this.open();
  }

  private open() {
    this.setStatus(this.attempt === 0 ? "connecting" : "reconnecting");
    const ws = this.opts.connect(this.opts.url);
    this.socket = ws;
    ws.onopen = () => {
      if (this.socket !== ws) return;
      this.attempt = 0;
      this.setStatus("open");
      this.authed = false;
      const publicChannels = [...this.subs.entries()].filter(([ch]) => !isPrivateChannel(ch));
      for (const [, sub] of publicChannels) sub.active = true;
      if (publicChannels.length > 0) this.send({ op: "subscribe", args: publicChannels.map(([ch]) => ch) });
      for (const [ch, sub] of this.subs) {
        if (isPrivateChannel(ch)) sub.active = false;
        else if (ch.startsWith("depth:")) this.setSyncing(ch, sub, true); // until its snapshot
      }
      const token = this.opts.token?.() || this.authedToken;
      if (token) {
        this.authedToken = token;
        this.sendAuth();
      }
    };
    ws.onmessage = (ev) => {
      if (this.socket !== ws) return;
      let msg: WsReply & Partial<MarketPush & PrivatePush>;
      try {
        msg = JSON.parse(String(ev.data));
      } catch {
        return;
      }
      this.handle(msg);
    };
    ws.onclose = () => {
      if (this.socket !== ws) return;
      this.socket = null;
      this.authed = false;
      for (const sub of this.subs.values()) sub.active = false;
      if (this.closed) return;
      this.scheduleReconnect();
    };
    ws.onerror = () => {
      // onclose follows and reconnects.
    };
  }

  private scheduleReconnect() {
    this.setStatus("reconnecting");
    const base = Math.min(30_000, 1000 * 2 ** this.attempt);
    this.attempt++;
    const jitter = base * 0.3 * (this.opts.random() * 2 - 1);
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => this.open(), Math.max(250, base + jitter));
  }

  private restart() {
    const ws = this.socket;
    this.socket = null;
    ws?.close(1000, "restart");
    if (!this.closed && this.subs.size > 0) this.open();
  }

  private send(msg: unknown) {
    if (this.socket?.readyState === OPEN) this.socket.send(JSON.stringify(msg));
  }

  private sendAuth() {
    if (this.socket?.readyState === OPEN && this.authedToken) this.send({ op: "auth", token: this.authedToken });
  }

  private activate(channel: string, sub: Sub) {
    if (sub.active || this.socket?.readyState !== OPEN) return;
    if (isPrivateChannel(channel) && !this.authed) return; // after auth
    sub.active = true;
    if (channel.startsWith("depth:")) this.setSyncing(channel, sub, true);
    const msg: Record<string, unknown> = { op: "subscribe", args: [channel] };
    if (isPrivateChannel(channel) && this.lastSeq > 0) msg.last_seq = this.lastSeq;
    this.send(msg);
  }

  private handle(msg: WsReply & Partial<MarketPush & PrivatePush>) {
    if (msg.op) {
      this.reply(msg);
      return;
    }
    if (typeof msg.channel !== "string") return;
    const sub = this.subs.get(msg.channel);
    if (!sub) return;
    if (isPrivateChannel(msg.channel)) {
      if (typeof msg.seq === "number") {
        if (msg.seq <= this.lastSeq) return; // a replay already seen
        this.lastSeq = msg.seq;
      }
    } else if (msg.channel.startsWith("depth:") && !this.followsDepth(msg.channel, sub, msg as MarketPush)) {
      return;
    }
    this.deliver(msg.channel, sub, msg as MarketPush | PrivatePush);
  }

  // followsDepth checks a depth message against the book's sequence.
  private followsDepth(channel: string, sub: Sub, msg: MarketPush): boolean {
    if (msg.type === "snapshot") {
      sub.seq = msg.seq ?? 0;
      this.setSyncing(channel, sub, false);
      return true;
    }
    if (sub.syncing) return false; // waiting for the snapshot
    if ((msg.prev_seq ?? 0) !== (sub.seq ?? 0)) {
      this.setSyncing(channel, sub, true);
      this.send({ op: "unsubscribe", args: [channel] });
      this.send({ op: "subscribe", args: [channel] });
      return false;
    }
    sub.seq = msg.seq ?? sub.seq;
    return true;
  }

  private deliver(channel: string, sub: Sub, msg: MarketPush | PrivatePush) {
    if (this.hidden && isHighFrequency(channel)) {
      sub.held = msg;
      return;
    }
    for (const fn of sub.listeners) fn(msg);
  }

  private reply(msg: WsReply) {
    switch (msg.op) {
      case "ping":
        this.send({ op: "pong" });
        return;
      case "auth":
        if (msg.ok) {
          this.authed = true;
          const privateChannels = [...this.subs.entries()].filter(([ch, sub]) => isPrivateChannel(ch) && !sub.active);
          if (privateChannels.length > 0) {
            for (const [, sub] of privateChannels) sub.active = true;
            const m: Record<string, unknown> = { op: "subscribe", args: privateChannels.map(([ch]) => ch) };
            if (this.lastSeq > 0) m.last_seq = this.lastSeq;
            // Without a sequence the server cannot replay what happened
            // before this subscription (the page's first seconds): once it
            // is in place, the private data reloads once.
            else this.catchUp = true;
            this.send(m);
          }
        } else if (msg.code === "AUTH_TOKEN_EXPIRED") {
          this.renewToken();
        }
        return;
      case "error":
        if (msg.code === "AUTH_TOKEN_EXPIRED") this.renewToken();
        return;
      case "subscribe":
        if (msg.ok && this.catchUp && (msg.args ?? []).some((ch) => isPrivateChannel(ch))) {
          this.catchUp = false;
          for (const fn of this.resyncListeners) fn(msg.args ?? []);
        }
        return;
      case "resync":
        for (const fn of this.resyncListeners) fn(msg.args ?? []);
        return;
    }
  }

  // renewToken refreshes the access token and authenticates again within
  // the server's 60-second grace.
  private renewToken() {
    void this.opts.refresh?.().then((token) => {
      if (token) {
        this.authedToken = token;
        this.sendAuth();
      }
    });
  }

  private setVisible(visible: boolean) {
    const wasHidden = this.hidden;
    this.hidden = !visible;
    if (!wasHidden || this.hidden) return;
    for (const [channel, sub] of this.subs) {
      if (channel.startsWith("depth:") && sub.held && (sub.held as MarketPush).type !== "snapshot") {
        // Updates were dropped: start the book again from a snapshot.
        sub.held = undefined;
        if (sub.active) {
          this.setSyncing(channel, sub, true);
          this.send({ op: "unsubscribe", args: [channel] });
          this.send({ op: "subscribe", args: [channel] });
        }
        continue;
      }
      const held = sub.held;
      sub.held = undefined;
      if (held) for (const fn of sub.listeners) fn(held);
    }
  }
}
