import type { AuthConfig } from "../domain/context.js";
import { loginLocalePath, t as translate } from "../domain/locale.js";
import { LanguageSelect, useLocale } from "./LanguageSelect.js";
import { Brand, ErrorNotice } from "./shared.js";
import { buttonStyles } from "./ui.js";

/** Public presentation only. Passwords are handled by the shared hosted account flow. */
export function Login({
  config,
  checking,
  error,
  retryLogout,
  loginHref,
}: {
  config: AuthConfig;
  checking: boolean;
  error: string;
  retryLogout: (() => Promise<void>) | undefined;
  loginHref?: string;
}) {
  const locale = useLocale();
  return (
    <div className="landing min-h-dvh flex flex-col bg-paper">
      <a
        href="#landing-main"
        className="sr-only focus:not-sr-only focus:absolute focus:z-20 focus:bg-paper focus:p-3"
      >
        {translate("К содержимому")}
      </a>
      <header className="landing-header flex items-center justify-between gap-4 mx-auto w-full max-w-320 px-10 py-6 mobile:px-5 mobile:py-4">
        <Brand />
        <LanguageSelect compact />
      </header>
      <main
        id="landing-main"
        tabIndex={-1}
        className="landing-main grid grid-cols-[1.1fr_1fr] items-center gap-14 mx-auto w-full max-w-320 flex-1 px-10 py-10 mobile:grid-cols-1 mobile:gap-6 mobile:px-5 mobile:pt-5 mobile:pb-8"
      >
        <section className="max-w-145" aria-labelledby="login-title">
          <h1
            id="login-title"
            className="text-[clamp(2.5rem,4.5vw,4.75rem)] leading-[1.06] font-semibold tracking-tight mb-6 mobile:text-4xl mobile:mb-4"
          >
            {translate("Задачи, код и решения вместе.")}
          </h1>
          <p className="text-lg leading-relaxed text-muted max-w-120 mb-8 mobile:text-base mobile:mb-6">
            {translate(
              "Gitmora связывает сессии агентов, коммиты и задачи в одну историю. Находите решения команды и продолжайте работу с нужным контекстом.",
            )}
          </p>
          <ErrorNotice>
            {retryLogout ? (
              <>
                {translate("Не удалось завершить сессию.")} {translate(error)}
              </>
            ) : (
              error
            )}
          </ErrorNotice>
          {retryLogout ? (
            <button
              className={buttonStyles.secondary}
              onClick={() => void retryLogout()}
            >
              {translate("Повторить выход")}
            </button>
          ) : checking ? (
            <p role="status" className="text-sm text-muted">
              {translate("Подключаемся…")}
            </p>
          ) : config.enabled ? (
            <>
              <a
                className={`${buttonStyles.primary} action-link min-w-36`}
                href={loginLocalePath(loginHref ?? "/v1/auth/login", locale)}
              >
                {translate("Войти")}
              </a>
              {config.registration_enabled ? (
                <p className="text-sm mt-4 mb-0 text-muted">
                  {translate("Впервые здесь?")}{" "}
                  <a href={loginLocalePath("/v1/auth/register", locale)}>
                    {translate("Создать аккаунт")}
                  </a>
                </p>
              ) : (
                <p className="text-sm mt-4 mb-0 text-muted">
                  {translate(
                    "За доступом обратитесь к администратору команды.",
                  )}
                </p>
              )}
            </>
          ) : (
            <p role="status" className="text-sm text-muted">
              {translate(
                "Вход пока недоступен. Обратитесь к администратору команды.",
              )}
            </p>
          )}
        </section>
        <figure
          className="landing-preview m-0 min-w-0"
          aria-label={translate(
            "Пример: задача, сессия агента, коммит и merge request связаны между собой",
          )}
        >
          <svg
            className="landing-lines absolute inset-0 w-full h-full text-control"
            viewBox="0 0 480 340"
            fill="none"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <path
              d="M100 72H352V156H108V258H356"
              stroke="currentColor"
              strokeWidth="1.5"
            />
            <circle cx="100" cy="72" r="5" fill="currentColor" />
            <circle cx="352" cy="156" r="5" fill="currentColor" />
            <circle cx="108" cy="258" r="5" fill="currentColor" />
          </svg>
          <ol className="landing-history relative grid grid-cols-2 gap-x-8 gap-y-12 list-none m-0 p-6 mobile:p-4 mobile:gap-x-4 mobile:gap-y-4">
            {(
              [
                [translate("Задача"), translate("Обновить вход")],
                [
                  translate("Сессия агента"),
                  translate("Обсуждение и изменения"),
                ],
                [translate("Коммит"), translate("Изменения в коде")],
                ["Merge request", translate("Готово к проверке")],
              ] as const
            ).map(([kind, title], index) => (
              <li
                key={kind}
                className={`relative border border-line rounded-lg bg-paper px-4 py-5 shadow-sm min-w-0 mobile:px-3 mobile:py-3 ${index % 2 ? "mt-12 -mb-12 mobile:mt-4 mobile:-mb-4" : ""}`}
              >
                <span className="text-sm text-muted block mb-2">{kind}</span>
                <strong className="font-medium text-base block leading-snug wrap-anywhere">
                  {title}
                </strong>
              </li>
            ))}
          </ol>
          <figcaption className="relative text-sm text-muted mt-14 px-6 mobile:mt-6 mobile:px-4">
            {translate("Пример: работа над входом")}
          </figcaption>
        </figure>
      </main>
    </div>
  );
}
