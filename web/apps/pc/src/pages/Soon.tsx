import { Button, EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router";

/**
 * Soon stands in for a page phase 4 B2 builds: its title, and a link to the
 * same page of the previous site (/h5/), where everything still works.
 */
export default function Soon({ title, legacy, bare }: { title: string; legacy: string; bare?: boolean }) {
  const { t } = useTranslation();
  const params = useParams();
  const path = legacy.replace(/:(\w+)/g, (_, k: string) => params[k] ?? "");
  const body = (
    <EmptyState
      title={t(title)}
      description={`${t("common.comingSoon")} · ${t("pc.legacyHint")}`}
      action={
        <Button asChild size="sm">
          <a href={`/h5${path}`}>{t("pc.legacyLink")}</a>
        </Button>
      }
    />
  );
  if (bare) return body;
  return <div className="mx-auto max-w-[1440px] px-6 py-16">{body}</div>;
}
