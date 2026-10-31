import { t as translate } from "../../domain/locale.js";
import { useEffect, useRef, useState } from "react";
import type { Me } from "../../domain/context.js";
import { readableError } from "../../domain/context.js";
import type { CLIView } from "../../domain/cli.js";
import {
  checkCLIView,
  cliDecision,
  cliLoginPath,
  cliScopeNames,
} from "../../domain/cli.js";
import { LatestRequest } from "../../application/latest.js";
import { useMutation } from "../../application/useMutation.js";
import type { ApiClient } from "../../transport/api.js";
import { Brand, ErrorNotice, aborted, date } from "../shared.js";
import { ConfirmAction } from "../SettingsShared.js";
import { buttonStyles } from "../ui.js";

/** Explicit current-account consent; neither a public code nor an API bearer can approve itself. */
export function CLIConsent({
  client,
  me,
  code,
  onClose,
}: {
  client: ApiClient;
  me: Me;
  code: string;
  onClose: () => void;
}) {
  const [view, setView] = useState<CLIView | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [prepared, setPrepared] = useState<ReturnType<
    typeof cliDecision
  > | null>(null);
  const [requests] = useState(() => new LatestRequest());
  const live = useRef<CLIView | null>(null);
  const action = useMutation(client);
  async function load() {
    const request = requests.begin();
    live.current = null;
    setView(null);
    setPrepared(null);
    setError("");
    setLoading(true);
    try {
      const v = await client.request<CLIView>(
        "/v1/auth/cli/inspect",
        { user_code: code },
        request.signal,
      );
      if (!request.current()) return;
      checkCLIView(v, me.user.id);
      live.current = v;
      setView(v);
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => {
      requests.cancel();
      live.current = null;
    };
  }, [client, code, me.token_id, requests]);
  useEffect(() => {
    if (!view || view.status !== "pending") return;
    const remaining = Date.parse(view.expires_at) - Date.now();
    const timer = setTimeout(
      () => {
        requests.cancel();
        live.current = null;
        setView(null);
        setPrepared(null);
        setError(
          "Окно подтверждения истекло. Проверьте состояние входа в терминале.",
        );
      },
      Math.max(0, remaining),
    );
    return () => clearTimeout(timer);
  }, [view, requests]);
  function prepare(approve: boolean) {
    if (!view) return;
    try {
      setPrepared(cliDecision(code, view, me.user.id, approve));
      setError("");
    } catch (e) {
      setError(readableError(e));
    }
  }
  async function decide() {
    if (!prepared || !view) return;
    const current = view;
    const response = await action.run<CLIView>(
      "POST",
      "/v1/auth/cli/decide",
      prepared,
    );
    if (!response) return;
    if (
      !live.current ||
      live.current.id !== current.id ||
      live.current.fingerprint !== prepared.expected_fingerprint
    )
      return;
    try {
      const v = response.value;
      checkCLIView(v, me.user.id);
      if (
        v.id !== current.id ||
        v.fingerprint !== current.fingerprint ||
        v.status !== (prepared.approve ? "approved" : "denied")
      )
        throw new Error("Ответ не совпал с подтверждённым запросом.");
      live.current = v;
      setView(v);
      setPrepared(null);
    } catch (e) {
      live.current = null;
      setView(null);
      setPrepared(null);
      setError(readableError(e));
    }
  }
  const allowed = !!view && view.scopes.every((s) => me.scopes.includes(s));
  return (
    <main className="cli-consent min-h-[100dvh] bg-canvas text-ink p-10 mobile:p-4">
      <div className="mx-auto max-w-[880px]">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <Brand />
          <button
            className={buttonStyles.secondary}
            disabled={action.pending}
            onClick={onClose}
          >
            {translate("Вернуться в Gitmora")}
          </button>
        </div>
        <section
          className="mt-10 bg-paper border border-line rounded-lg p-8 mobile:p-5"
          aria-label={translate("Подтверждение входа CLI")}
        >
          <h1>{translate("Подключить терминал")}</h1>
          <p className="text-muted">
            {translate("Учётная запись:")}{" "}
            <strong className="text-ink">{me.user.display_name}</strong>
          </p>
          <p>
            {translate(
              "Сверьте этот код с кодом в вашем терминале. Подтверждайте только вход, который начали сами.",
            )}
          </p>
          <div
            className="cli-human-code font-mono text-[32px] tracking-[3px] my-7 wrap-anywhere"
            role="group"
            aria-label={translate("Код входа CLI")}
          >
            {code}
          </div>
          <ErrorNotice>{error}</ErrorNotice>
          <button
            className={buttonStyles.secondary}
            disabled={loading || action.pending}
            onClick={() => void load()}
          >
            {translate("Обновить запрос CLI")}
          </button>
          {loading && (
            <p role="status" className="notice text-muted">
              {translate("Читаем условия подключения…")}
            </p>
          )}
          {view && (
            <div className="cli-request mt-7">
              <h2>
                {translate("Устройство:")} {view.label}
              </h2>
              <p className="text-muted">
                {translate(
                  "Этот ключ не добавляет членство в проектах и не меняет вашу роль.",
                )}
              </p>
              <h3>{translate("Запрашиваемые права")}</h3>
              <ul className="space-y-2 pl-5">
                {view.scopes.map((s) => (
                  <li key={s}>
                    {translate(cliScopeNames[s] ?? s)}{" "}
                    <span className="font-mono text-xs text-muted">({s})</span>
                  </li>
                ))}
              </ul>
              <dl className="grid grid-cols-[170px_minmax(0,1fr)] gap-x-4 gap-y-2 mt-7 text-sm mobile:grid-cols-[1fr]">
                <dt className="text-muted">{translate("Срок ключа")}</dt>
                <dd>
                  {view.ttl_seconds / 86400 >= 1
                    ? translate("{0} дн.", view.ttl_seconds / 86400)
                    : translate("{0} мин.", view.ttl_seconds / 60)}
                </dd>
                <dt className="text-muted">
                  {translate("Окно подтверждения")}
                </dt>
                <dd>{date(view.expires_at)}</dd>
                {view.credential_expires_at && (
                  <>
                    <dt className="text-muted">
                      {translate("Ключ действует до")}
                    </dt>
                    <dd>{date(view.credential_expires_at)}</dd>
                  </>
                )}
              </dl>
              <p className="notice text-muted mt-6">
                {translate(
                  "Доступ терминала сохранится после выхода из браузера. Отозвать ключ можно в разделе «Личные ключи». Отключение учётной записи или OIDC-привязки также закроет доступ.",
                )}
              </p>
              {view.status === "pending" ? (
                <>
                  <div className="flex flex-wrap gap-3 mt-7">
                    <button
                      className={buttonStyles.dangerOutline}
                      disabled={action.pending}
                      onClick={() => prepare(false)}
                    >
                      {translate("Отклонить запрос CLI…")}
                    </button>
                    <button
                      className={buttonStyles.primary}
                      disabled={!allowed || action.pending}
                      onClick={() => prepare(true)}
                    >
                      {translate("Разрешить вход CLI…")}
                    </button>
                  </div>
                  {!allowed && (
                    <p className="text-muted">
                      {translate(
                        "Текущая сессия не предоставляет все запрошенные права. Можно отклонить запрос.",
                      )}
                    </p>
                  )}
                </>
              ) : (
                <p className="cli-receipt notice text-ink" role="status">
                  {view.status === "approved"
                    ? translate(
                        "Подтверждение принято. Проверьте завершение входа в терминале.",
                      )
                    : view.status === "cancelled"
                      ? translate("Запрос отменён в терминале. Ключ не создан.")
                      : translate("Запрос отклонён. Ключ не создан.")}
                </p>
              )}
            </div>
          )}
          <a
            className="button inline-flex items-center justify-center rounded-[6px] px-4 py-3 min-h-11 [border:1px_solid_var(--color-control)] text-ink bg-paper no-underline mt-7"
            href={cliLoginPath(code)}
          >
            {translate("Войти через организацию заново")}
          </a>
        </section>
      </div>
      {prepared && view && (
        <ConfirmAction
          title={
            prepared.approve
              ? translate("Разрешить вход терминала?")
              : translate("Отклонить запрос терминала?")
          }
          tone={prepared.approve ? "primary" : "danger"}
          label={
            prepared.approve
              ? translate("Подтвердить вход CLI")
              : translate("Подтвердить отказ CLI")
          }
          pending={action.pending}
          error={action.error}
          onClose={() => setPrepared(null)}
          onConfirm={() => void decide()}
        >
          <p>
            {translate("Код:")} <strong className="font-mono">{code}</strong>
          </p>
          <p>
            {translate("Устройство:")} {view.label}
            {translate(". Учётная запись:")} {me.user.display_name}.
          </p>
          <p>
            {prepared.approve
              ? translate(
                  "Терминал получит перечисленные права на указанный срок. Ключ останется отдельным от браузерной сессии.",
                )
              : translate("Для новой попытки начните новый вход в терминале.")}
          </p>
        </ConfirmAction>
      )}
    </main>
  );
}
