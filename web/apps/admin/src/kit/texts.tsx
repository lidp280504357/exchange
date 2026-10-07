import type { AdminSchemas } from "@exchange/core/api/admin";
import { FormField, Input } from "@exchange/ui";
import { useTranslation } from "react-i18next";

type Texts = AdminSchemas["PlatformTexts"];

/**
 * TextsField edits a text in Simplified Chinese, Traditional Chinese
 * (G7b; empty, the sites show the Simplified) and English.
 */
export function TextsField({ label, value, onChange, disabled, long }: { label: string; value: Texts; onChange: (v: Texts) => void; disabled: boolean; long?: boolean }) {
  const { t } = useTranslation();
  const box = (lang: "zh-CN" | "zh-TW" | "en") =>
    long ? (
      <textarea
        value={value[lang] ?? ""}
        disabled={disabled}
        rows={2}
        placeholder={lang === "zh-TW" ? t("admin.platform.zhTWEmpty") : undefined}
        onChange={(e) => onChange({ ...value, [lang]: e.target.value })}
        className="w-full rounded-1 border border-line-1 bg-bg-1 px-2.5 py-1.5 text-sm text-fg-1 disabled:opacity-60"
        aria-label={`${label} ${lang}`}
      />
    ) : (
      <Input
        size="sm"
        value={value[lang] ?? ""}
        disabled={disabled}
        placeholder={lang === "zh-TW" ? t("admin.platform.zhTWEmpty") : undefined}
        onValueChange={(v) => onChange({ ...value, [lang]: v })}
        aria-label={`${label} ${lang}`}
      />
    );
  return (
    <FormField label={label}>
      <div className="grid gap-2 sm:grid-cols-3">
        <label className="flex flex-col gap-1 text-xs text-fg-3">
          {t("admin.platform.zh")}
          {box("zh-CN")}
        </label>
        {/* Optional, said where it is written (review EV): empty, the sites show the Simplified. */}
        <label className="flex flex-col gap-1 text-xs text-fg-3">
          {t("admin.platform.zhTWOptional")}
          {box("zh-TW")}
        </label>
        <label className="flex flex-col gap-1 text-xs text-fg-3">
          {t("admin.platform.en")}
          {box("en")}
        </label>
      </div>
    </FormField>
  );
}
