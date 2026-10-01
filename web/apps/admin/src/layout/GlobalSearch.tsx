import { ApiError } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Input, toast } from "@exchange/ui";
import { Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { errorToast } from "../kit/actions";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const TX = /^(0x)?[0-9a-f]{64}$/i;

/**
 * GlobalSearch finds what an ID, an email, a phone number or a
 * transaction hash names: a user opens on its page, an order in the
 * orders list, a transaction in the deposits list. ⌘K (Ctrl+K) puts the
 * cursor in it from anywhere.
 */
export function GlobalSearch({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        input.current?.focus();
        input.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const openUser = (id: string) => navigate(`/users/${id}`);
  const lookup = async (query: string): Promise<string | null> => {
    try {
      return adminData(await adminApi.GET("/admin/v1/users/lookup", { params: { query: { q: query } } })).user.id;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null;
      throw err;
    }
  };
  const run = async () => {
    const query = q.trim();
    if (!query) return;
    setBusy(true);
    try {
      if (TX.test(query) && can(admin, "withdrawals.read")) {
        navigate(`/deposits?tx_hash=${encodeURIComponent(query)}`);
        return;
      }
      if (UUID.test(query)) {
        const user = can(admin, "users.read") ? await lookup(query) : null;
        if (user) return openUser(user);
        const orders = adminData(await adminApi.GET("/admin/v1/orders", { params: { query: { order_id: query, limit: 1 } } }));
        if (orders.items.length > 0) return navigate(`/orders?order_id=${encodeURIComponent(query)}`);
      } else if ((query.includes("@") || query.startsWith("+")) && can(admin, "users.read")) {
        const user = await lookup(query);
        if (user) return openUser(user);
      }
      toast.info(t("admin.search.notFound", { q: query }));
    } catch (err) {
      errorToast(err);
    } finally {
      setBusy(false);
    }
  };
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
  return (
    <div className="w-full max-w-md">
      <Input
        ref={input}
        size="sm"
        value={q}
        onValueChange={setQ}
        onKeyDown={(e) => {
          if (e.key === "Enter") void run();
          if (e.key === "Escape") input.current?.blur();
        }}
        prefix={<Search size={14} className="text-fg-3" />}
        suffix={
          q ? undefined : (
            <kbd className="mr-2 hidden rounded-1 border border-line-2 px-1.5 font-sans text-xs text-fg-3 sm:inline">{mac ? "⌘K" : "Ctrl K"}</kbd>
          )
        }
        placeholder={busy ? t("admin.search.searching") : t("admin.search.placeholder")}
        aria-label={t("admin.common.search")}
        clearable
        onClear={() => setQ("")}
        boxClassName="bg-bg-0"
      />
    </div>
  );
}
