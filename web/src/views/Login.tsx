import type { AuthConfig } from "../domain/context.js";
import { loginLocalePath, t as translate } from "../domain/locale.js";
import { LanguageSelect, useLocale } from "./LanguageSelect.js";
import { ErrorNotice } from "./shared.js";
import { buttonStyles } from "./ui.js";
import { AuthStory } from "./AuthStory.js";
import { Icon } from "./Icon.js";

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
    <div className="landing auth-shell">
      <a
        href="#landing-main"
        className="sr-only focus:not-sr-only focus:absolute focus:z-20 focus:bg-paper focus:p-3"
      >
        {translate("К содержимому")}
      </a>
      <AuthStory />
      <main id="landing-main" tabIndex={-1} className="landing-main auth-main">
        <div className="auth-language">
          <LanguageSelect compact />
        </div>
        <section className="auth-card" aria-labelledby="login-title">
          <div className="auth-icon">
            <Icon name="context" />
          </div>
          <h1 id="login-title">{translate("История разработки под рукой.")}</h1>
          <p className="auth-description">
            {translate(
              "Gitmora связывает сессии агентов с коммитами и MR/PR. Находите решения команды и продолжайте работу с нужным контекстом.",
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
                className={`${buttonStyles.primary} action-link w-full`}
                href={loginLocalePath(loginHref ?? "/v1/auth/login", locale)}
              >
                {translate("Войти")}
              </a>
              {config.registration_enabled ? (
                <p className="text-sm mt-6 mb-0 text-center text-muted">
                  {translate("Впервые здесь?")}{" "}
                  <a href={loginLocalePath("/v1/auth/register", locale)}>
                    {translate("Создать аккаунт")}
                  </a>
                </p>
              ) : (
                <p className="text-sm mt-6 mb-0 text-center text-muted">
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
      </main>
    </div>
  );
}
