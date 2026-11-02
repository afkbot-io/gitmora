import { t } from "../domain/locale.js";
import { buttonStyles } from "./ui.js";
type Filters = {
  agent: string;
  model: string;
  role: string;
  file_path: string;
};

/** Less common filters stay behind one disclosure, while the main query remains a single field. */
export function SearchFilters({
  value,
  onChange,
  repository,
}: {
  value: Filters;
  onChange: (value: Filters) => void;
  repository: string;
}) {
  const active = Object.values(value).filter((item) => item.trim()).length;
  function update(key: keyof Filters, next: string) {
    onChange({ ...value, [key]: next });
  }
  return (
    <details className="mt-3 text-sm">
      <summary className="w-fit cursor-pointer py-2 text-muted">
        {t("Фильтры")}
        {active ? ` (${active})` : ""}
      </summary>
      <div className="grid grid-cols-2 gap-4 pt-3 mobile:grid-cols-1">
        <label className="m-0 flex min-w-0 flex-col gap-2">
          {t("Агент")}
          <input
            className="w-full min-w-0"
            aria-label={t("Агент")}
            value={value.agent}
            maxLength={120}
            list="search-agents"
            onChange={(e) => update("agent", e.target.value)}
          />
          <datalist id="search-agents">
            <option value="codex" />
            <option value="opencode" />
          </datalist>
        </label>
        <label className="m-0 flex min-w-0 flex-col gap-2">
          {t("Модель")}
          <input
            className="w-full min-w-0"
            aria-label={t("Модель")}
            value={value.model}
            maxLength={240}
            onChange={(e) => update("model", e.target.value)}
          />
        </label>
        <label className="m-0 flex min-w-0 flex-col gap-2">
          {t("Тип сообщения")}
          <select
            className="w-full min-w-0"
            aria-label={t("Тип сообщения")}
            value={value.role}
            onChange={(e) => update("role", e.target.value)}
          >
            <option value="">{t("Любой")}</option>
            <option value="user">{t("Пользователь")}</option>
            <option value="assistant">{t("Агент")}</option>
            <option value="tool">{t("Инструмент")}</option>
            <option value="system">{t("Сведения об источнике")}</option>
          </select>
        </label>
        <label className="m-0 flex min-w-0 flex-col gap-2">
          {t("Путь к файлу")}
          <input
            className="w-full min-w-0"
            aria-label={t("Путь к файлу")}
            value={value.file_path}
            maxLength={1024}
            disabled={!repository}
            placeholder={
              !repository ? t("Выберите репозиторий") : "src/main.go"
            }
            onChange={(e) => update("file_path", e.target.value)}
          />
        </label>
      </div>
      {active > 0 && (
        <button
          type="button"
          className={buttonStyles.text + " mt-3"}
          onClick={() =>
            onChange({ agent: "", model: "", role: "", file_path: "" })
          }
        >
          {t("Сбросить фильтры")}
        </button>
      )}
    </details>
  );
}
