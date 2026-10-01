import { EmptyState } from "@exchange/ui";
import { useTranslation } from "react-i18next";
import { Page } from "../../kit/Page";

/** Custody: the custodian's balances, fees, callbacks and reconciliation arrive with B6 (design §9). */
export default function Custody() {
  const { t } = useTranslation();
  return (
    <Page title={t("admin.custody.title")}>
      <EmptyState title={t("admin.custody.title")} description={t("admin.custody.soon")} />
    </Page>
  );
}
