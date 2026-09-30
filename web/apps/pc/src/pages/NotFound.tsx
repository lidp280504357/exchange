import { routes } from "@exchange/core";
import { Button, EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

export default function NotFound() {
  const { t } = useTranslation();
  return (
    <div className="mx-auto max-w-[1440px] px-6 py-24">
      <EmptyState
        title={t("state.notFoundTitle")}
        description={t("state.notFoundHint")}
        action={
          <Button asChild size="sm">
            <Link to={routes.home}>{t("nav.home")}</Link>
          </Button>
        }
      />
    </div>
  );
}
