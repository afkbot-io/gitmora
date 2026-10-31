import { useId, useSyncExternalStore } from "react";
import { currentLocale, t as translate } from "../domain/locale.js";

function subscribe(changed: () => void) {
  window.addEventListener("gitmora:locale-change", changed);
  return () => window.removeEventListener("gitmora:locale-change", changed);
}
export function useLocale() {
  return useSyncExternalStore(subscribe, currentLocale);
}
export function LanguageSelect({
  compact = false,
  hideLabel = false,
}: {
  compact?: boolean;
  hideLabel?: boolean;
}) {
  const locale = useLocale();
  const id = useId();
  return (
    <div className={compact ? "language-select m-0" : "max-w-sm"}>
      <label
        htmlFor={id}
        className={compact || hideLabel ? "sr-only" : "block mb-2"}
      >
        {translate("Язык")}
      </label>
      <select
        id={id}
        name="language"
        value={locale}
        onChange={(event) =>
          window.dispatchEvent(
            new CustomEvent("gitmora:locale-select", {
              detail: event.target.value,
            }),
          )
        }
        className={
          compact
            ? "border-transparent bg-transparent text-sm min-h-11"
            : "w-full"
        }
      >
        <option value="ru" lang="ru">
          {translate("Русский")}
        </option>
        <option value="en" lang="en">
          English
        </option>
      </select>
    </div>
  );
}
