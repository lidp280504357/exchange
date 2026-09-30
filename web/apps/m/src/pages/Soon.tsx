import { Button, EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router";

/** Soon stands in for a page phase 4 B3 builds, with a link to the previous site. */
export default function Soon({ title, legacy }: { title: string; legacy: string }) {
  const { t } = useTranslation();
  const params = useParams();
  const path = legacy.replace(/:(\w+)/g, (_, k: string) => params[k] ?? "");
  return (
    <div className="px-4 py-12">
      <EmptyState
        title={t(title)}
        description={`${t("common.comingSoon")} · ${t("m.legacyHint")}`}
        action={
          <Button asChild size="md">
            <a href={`https://astras.vip/h5${path}`}>{t("m.legacyLink")}</a>
          </Button>
        }
      />
    </div>
  );
}
