import { ApiError } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Input, toast } from "@exchange/ui";
import { Search } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router";
import { errorToast } from "../kit/actions";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const TX = /^(0x)?[0-9a-f]{64}$/i;

/**
 * GlobalSearch finds what an ID, an email, a phone number or a
 * transaction hash names (design §10.1): a user opens in the drawer, an
 * order in the orders list, a transaction in the deposits list.
 */
export function GlobalSearch({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);

  const [, setParams] = useSearchParams();
  // The drawer opens over the current page, its filters kept.
  const openUser = (id: string) =>
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("user", id);
      return next;
    });
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
  return (
    <div className="w-full max-w-md">
      <Input
        size="sm"
        value={q}
        onValueChange={setQ}
        onKeyDown={(e) => {
          if (e.key === "Enter") void run();
        }}
        prefix={<Search size={14} className="text-fg-3" />}
        placeholder={busy ? t("admin.search.searching") : t("admin.search.placeholder")}
        aria-label={t("admin.common.search")}
        clearable
        onClear={() => setQ("")}
      />
    </div>
  );
}
