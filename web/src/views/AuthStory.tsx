import { t } from "../domain/locale.js";
import { Brand } from "./shared.js";

/** Presentation shared visually with the hosted account theme; no account state. */
export function AuthStory() {
  return (
    <aside className="auth-aside">
      <a href="/" className="relative z-1 w-fit text-ink no-underline">
        <Brand />
      </a>
      <div className="auth-story">
        <span className="auth-pill">{t("Контекст вашей команды")}</span>
        <h2>
          {t("От диалога")}
          <br />
          {t("к коммиту.")}
          <br />
          <em>{t("Вся история рядом.")}</em>
        </h2>
        <p>
          {t(
            "Находите решения в переписке с агентами и возвращайтесь к коду с нужным контекстом.",
          )}
        </p>
        <ol className="auth-diagram list-none p-0">
          {(
            [
              ["Диалог", "Запросы, ответы и действия агента"],
              ["Коммит", "Код и история его появления"],
              ["MR / PR", "Переписка к конкретному изменению"],
            ] as const
          ).map(([title, description], index) => (
            <li key={title}>
              <span>{String(index + 1).padStart(2, "0")}</span>
              <strong>{t(title)}</strong>
              <small>{t(description)}</small>
            </li>
          ))}
        </ol>
      </div>
      <span className="auth-footer">{t("Сессии. Коммиты. Контекст.")}</span>
    </aside>
  );
}
