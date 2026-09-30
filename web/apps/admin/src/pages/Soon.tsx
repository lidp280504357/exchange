import { Button, EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";

/** Soon stands in for a section phase 4 B5 rebuilds, with a link to the previous console. */
export default function Soon({ section, legacy }: { section: string; legacy: string }) {
  const { t } = useTranslation();
  return (
    <EmptyState
      title={t(`admin.nav.${section}`)}
      description={t("admin.soon")}
      action={
        <Button asChild size="sm">
          <a href={`https://astras.vip/admin/${legacy}`}>{t("admin.legacy")}</a>
        </Button>
      }
    />
  );
}
