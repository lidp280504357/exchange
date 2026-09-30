import { Spinner } from "@exchange/ui";
import { RotateCcw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { TextButton } from "../../auth/parts/TextButton";

export type LoadMoreProps = {
  hasMore: boolean;
  /** The next page is on its way. */
  loading: boolean;
  /** The next page failed: a retry replaces the automatic load. */
  failed: boolean;
  onMore: () => void;
  /** Rows shown so far ("no more" only under a list that has rows). */
  count: number;
};

/**
 * LoadMore sits under an infinite list: when it comes near the screen it
 * asks for the next page, and keeps asking while it stays in view (a
 * short filtered list); then it says the list is complete, or offers a
 * retry when a page fails.
 */
export function LoadMore({ hasMore, loading, failed, onMore, count }: LoadMoreProps) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  const more = useRef(onMore);
  more.current = onMore;
  const [near, setNear] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((entries) => setNear(entries.some((e) => e.isIntersecting)), { rootMargin: "240px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    if (near && hasMore && !loading && !failed) more.current();
  }, [near, hasMore, loading, failed]);

  return (
    <div ref={ref} className="flex min-h-12 items-center justify-center text-xs text-fg-3">
      {failed ? (
        <span role="alert" className="flex items-center gap-2 text-danger">
          {t("mAccount.list.failed")}
          <TextButton tone="danger" onClick={onMore} className="px-2">
            <RotateCcw size={14} /> {t("common.retry")}
          </TextButton>
        </span>
      ) : loading ? (
        <span className="flex items-center gap-2">
          <Spinner size={14} /> {t("common.loading")}
        </span>
      ) : !hasMore && count > 0 ? (
        t("mAccount.list.end")
      ) : null}
    </div>
  );
}
