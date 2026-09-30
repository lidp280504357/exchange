import { EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { usePageHeader } from "../layout/header";

/** Soon stands in for a page of phase 4 B3 still being built. */
export default function Soon({ title }: { title: string }) {
  const { t } = useTranslation();
  usePageHeader({ title: t(title) }, [title]);
  return <EmptyState title={t(title)} description={t("m.soon")} />;
}
