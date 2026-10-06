import type { InfiniteData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { routes } from "../routes";
import { filterNotices, noticeCategory, noticeKeys, noticeLink, noticeRisk, readInPages, type Notice, type NoticePage } from "./notifications";

const notice = (id: string, type: string, read = false): Notice => ({ id, type, title: type, body: "", data: {}, read, created_at: "2026-09-30T10:00:00Z" });

const pages = (): InfiniteData<NoticePage> => ({
  pages: [
    { items: [notice("a", "NEW_DEVICE_LOGIN"), notice("b", "DEPOSIT_CREDITED"), notice("c", "WELCOME", true)], next_cursor: "c", unread_count: 3 },
    { items: [notice("d", "WITHDRAWAL_REQUESTED")], next_cursor: null, unread_count: 3 },
  ],
  pageParams: ["", "c"],
});

describe("notifications", () => {
  it("keeps every key under the notifications root, which pushes invalidate", () => {
    expect(noticeKeys.list[0]).toBe("notifications");
    expect(noticeKeys.unread[0]).toBe("notifications");
    expect(noticeKeys.all).toEqual(["notifications"]);
  });

  it("groups types into categories", () => {
    expect(noticeCategory("PASSWORD_CHANGED")).toBe("security");
    expect(noticeCategory("TOTP_CHANGED")).toBe("security");
    expect(noticeCategory("DEPOSIT_UNCLAIMED")).toBe("assets");
    expect(noticeCategory("WITHDRAWAL_FAILED")).toBe("assets");
    expect(noticeCategory("MARGIN_LIQUIDATED")).toBe("assets");
    expect(noticeCategory("STATUS_CHANGED")).toBe("system");
    expect(noticeCategory("SOMETHING_NEW")).toBe("system");
  });

  it("links a notice to the page it is about", () => {
    expect(noticeLink({ type: "NEW_DEVICE_LOGIN" })).toBe(routes.sessions);
    expect(noticeLink({ type: "IDENTITY_CHANGED" })).toBe(routes.security);
    expect(noticeLink({ type: "DEPOSIT_CREDITED" })).toBe(routes.deposit);
    expect(noticeLink({ type: "WITHDRAWAL_REJECTED" })).toBe(routes.withdraw);
    expect(noticeLink({ type: "WELCOME" })).toBe(routes.assets);
    expect(noticeLink({ type: "MARGIN_WARNED" })).toBe(routes.margin);
    expect(noticeLink({ type: "STATUS_CHANGED" })).toBeNull();
    expect(noticeLink({ type: "BROADCAST", data: { broadcast_id: "b", link: "/announcements/maintenance" } })).toBe("/announcements/maintenance");
    expect(noticeLink({ type: "BROADCAST", data: { broadcast_id: "b", link: "https://evil.example.com" } })).toBeNull();
    expect(noticeLink({ type: "BROADCAST", data: { broadcast_id: "b" } })).toBeNull();
    expect(noticeLink({ type: "BROADCAST", data: { broadcast_id: "b", link: "//evil.example.com" } })).toBeNull();
  });

  it("filters by category and unread", () => {
    const items = pages().pages.flatMap((p) => p.items);
    expect(filterNotices(items, "all").map((n) => n.id)).toEqual(["a", "b", "c", "d"]);
    expect(filterNotices(items, "assets").map((n) => n.id)).toEqual(["b", "d"]);
    expect(filterNotices(items, "system", true)).toEqual([]);
    expect(filterNotices(items, "all", true).map((n) => n.id)).toEqual(["a", "b", "d"]);
  });

  it("marks read in the cached pages and lowers the count", () => {
    const some = readInPages(pages(), ["a", "c", "d"])!;
    expect(some.pages[0]!.items.map((n) => n.read)).toEqual([true, false, true]);
    expect(some.pages[1]!.items[0]!.read).toBe(true);
    // c was read already: two changed.
    expect(some.pages.map((p) => p.unread_count)).toEqual([1, 1]);
    const all = readInPages(pages(), "all")!;
    expect(all.pages.every((p) => p.unread_count === 0 && p.items.every((n) => n.read))).toBe(true);
    expect(readInPages(undefined, "all")).toBeUndefined();
  });
});

describe("noticeRisk", () => {
  it("warns of a margin level under the warning line and marks a liquidation a danger", () => {
    expect(noticeRisk("MARGIN_WARNED")).toBe("warn");
    expect(noticeRisk("MARGIN_LIQUIDATING")).toBe("danger");
    expect(noticeRisk("MARGIN_LIQUIDATED")).toBe("danger");
    expect(noticeRisk("DEPOSIT_CREDITED")).toBeNull();
  });
});
