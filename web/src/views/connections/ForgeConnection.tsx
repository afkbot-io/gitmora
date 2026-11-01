import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { repositoryURL, repositoryProviderID } from "../../domain/context.js";
import type { Repository } from "../../domain/context.js";
import type { ForgeStatus } from "../../domain/settings.js";
import { diagnostic, providerLabel } from "../../domain/settings.js";
import { useMutation } from "../../application/useMutation.js";
import type { ApiClient } from "../../transport/api.js";
import { useConnection } from "./useConnection.js";
import {
  ConfirmAction,
  MutationNotice,
  MutationForm,
} from "../SettingsShared.js";
import { ErrorNotice, date } from "../shared.js";

export function ForgeConnection({
  client,
  repository,
  canRead,
  canManage,
}: {
  client: ApiClient;
  repository: Repository;
  canRead: boolean;
  canManage: boolean;
}) {
  const github = repository.provider === "github";
  const label = github ? "GitHub" : "GitLab";
  const [anonymous, setAnonymous] = useState(github);
  const path = `/v1/repositories/${repository.id}/forge`;
  const status = useConnection<ForgeStatus>(client, path, canRead);
  const action = useMutation(client);
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [token, setToken] = useState("");
  const [webhook, setWebhook] = useState("");
  const [mode, setMode] = useState<ForgeStatus["webhook_mode"]>("disabled");
  useEffect(() => {
    if (!canManage) {
      setEditing(false);
      setToken("");
      setWebhook("");
      setMode("disabled");
      setRemoving(false);
    }
  }, [canManage]);
  async function save(event: FormEvent) {
    event.preventDefault();
    const body = {
      ...(github && anonymous ? { anonymous: true } : { api_token: token }),
      webhook_mode: github ? "disabled" : mode,
      ...(mode === "signature"
        ? { webhook_signing_token: webhook }
        : mode === "legacy"
          ? { legacy_webhook_token: webhook }
          : {}),
    };
    setToken("");
    setWebhook("");
    const saved = await action.run<ForgeStatus>("PUT", path, body);
    if (saved) {
      status.setValue(saved.value);
      setEditing(false);
      action.setMessage("Соединение сохранено после проверки доступа.");
    }
  }
  async function sync() {
    if (await action.run("POST", path + "/sync")) {
      action.setMessage(
        "Сверка запрошена. Результат появится после работы фонового процесса.",
      );
      await status.refresh();
    }
  }
  async function remove() {
    if (await action.run("DELETE", path)) {
      setRemoving(false);
      status.clear();
      action.setMessage("Соединение отключено. Сохранённые ключи удалены.");
      await status.refresh();
    }
  }
  const value = status.value;
  return (
    <div className="connection-detail mt-6">
      <dl className={sharedStyles.connectionScope}>
        <dt>{translate("Сервер")}</dt>
        <dd>{repositoryURL(repository)}</dd>
        <dt>
          {github
            ? translate("Репозиторий GitHub")
            : translate("Проект GitLab")}
        </dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {providerLabel(repositoryProviderID(repository))}
        </dd>
      </dl>
      <ErrorNotice>{status.error}</ErrorNotice>
      {status.loading && (
        <p role="status">{translate("Читаем состояние {0}…", label)}</p>
      )}
      {status.missing && (
        <p className="muted text-muted">
          {translate("Соединение {0} ещё не настроено.", label)}
        </p>
      )}
      {!canRead && !value && (
        <p className="muted text-muted">
          {translate("Состояние соединения недоступно без integration:read.")}
        </p>
      )}
      {value && (
        <>
          <div
            className={`
            connection-summary flex flex-wrap justify-between gap-3 px-0 py-4
            border-t border-line [&_strong]:font-medium [&_span]:text-muted
          `}
          >
            <strong>
              {!value.enabled
                ? translate("Отключено")
                : value.blocked
                  ? translate("Требует вмешательства")
                  : translate("Соединение включено")}
            </strong>
            <span className="small text-sm">
              {translate("Поколение")} {value.generation}
            </span>
          </div>
          <dl className={sharedStyles.connectionScope}>
            <dt>{translate("Последняя сверка")}</dt>
            <dd>
              {value.last_synced_at
                ? date(value.last_synced_at)
                : translate("Ещё не завершалась")}
            </dd>
            <dt>{translate("Следующая попытка")}</dt>
            <dd>{date(value.next_sync_at)}</dd>
            {github && (
              <>
                <dt>{translate("Способ доступа")}</dt>
                <dd>
                  {value.anonymous
                    ? translate("Публичный доступ")
                    : translate("Токен чтения")}
                </dd>
              </>
            )}
            <dt>{translate("Диагностика")}</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {diagnostic(value.last_error_code)}
            </dd>
            {value.enabled && value.webhook_mode === "disabled" && (
              <>
                <dt>{translate("Синхронизация")}</dt>
                <dd>{translate("Только API, вебхуки отключены")}</dd>
              </>
            )}
            {value.enabled && value.webhook_mode !== "disabled" && (
              <>
                <dt>{translate("Вебхук GitLab")}</dt>
                <dd className="mono font-mono text-xs wrap-anywhere">
                  {location.origin + value.webhook_path}
                </dd>
                <dt>{translate("Проверка вебхука")}</dt>
                <dd>
                  {value.webhook_mode === "signature"
                    ? translate("Подпись HMAC")
                    : translate("Секретный заголовок")}
                </dd>
              </>
            )}
          </dl>
          {value.streams?.length > 0 && (
            <div
              className={`
                stream-list border-t border-line mx-0 my-6 [&_article]:grid
                [&_article]:grid-cols-[1fr_1fr] [&_article]:[gap:8px_20px] [&_article]:px-0 [&_article]:py-4
                [&_article]:border-b border-line [&_article]:text-xs [&_article]:text-muted
                [&_strong]:text-ink [&_strong]:font-medium mobile:[&_article]:grid-cols-[1fr]
              `}
              role="region"
              aria-label={translate("Потоки синхронизации {0}", label)}
            >
              {value.streams.map((stream) => (
                <article key={stream.kind}>
                  <strong>{stream.kind}</strong>
                  <span>
                    {stream.active
                      ? translate("Страница {0}", stream.next_page)
                      : translate("Нет активного окна")}
                  </span>
                  <span>
                    {translate("Завершение:")}{" "}
                    {stream.last_complete_at
                      ? date(stream.last_complete_at)
                      : translate("Ещё не завершалось")}
                  </span>
                  <span className="mono font-mono text-xs wrap-anywhere">
                    {diagnostic(stream.error_code)}
                  </span>
                </article>
              ))}
            </div>
          )}
        </>
      )}
      <MutationNotice action={action} />
      <div className="actions flex gap-3 flex-wrap items-center">
        {canRead && (
          <button
            className={buttonStyles.secondary}
            disabled={status.loading}
            onClick={() => void status.refresh()}
          >
            {translate("Обновить состояние {0}", label)}
          </button>
        )}
        {canManage && (
          <>
            <button
              className={buttonStyles.secondary}
              onClick={() => {
                setEditing(!editing);
                setToken("");
                setWebhook("");
                setMode(value?.webhook_mode ?? "disabled");
                setAnonymous(
                  github && (value ? value.anonymous === true : true),
                );
              }}
            >
              {editing
                ? translate("Отменить настройку")
                : value?.enabled
                  ? translate("Изменить соединение {0}", label)
                  : translate("Настроить {0}", label)}
            </button>
            {value?.enabled && (
              <>
                <button
                  className={buttonStyles.secondary}
                  disabled={action.pending}
                  onClick={() => void sync()}
                >
                  {translate("Запросить сверку {0}", label)}
                </button>
                <button
                  className={sharedStyles.destructiveText}
                  onClick={() => setRemoving(true)}
                >
                  {translate("Отключить {0}", label)}
                </button>
              </>
            )}
          </>
        )}
      </div>
      {canManage && editing && (
        <MutationForm
          pending={action.pending}
          className={sharedStyles.setup}
          onSubmit={(event) => void save(event)}
        >
          <h3>{translate("Доступ к {0}", label)}</h3>
          {github ? (
            <>
              <p className="text-sm text-muted leading-relaxed">
                {translate(
                  "Для публичного репозитория можно начать без токена. Для регулярной синхронизации используйте отдельный токен чтения этого репозитория.",
                )}
              </p>
              <label className="checkbox">
                <input
                  type="checkbox"
                  checked={anonymous}
                  onChange={(event) => {
                    setAnonymous(event.target.checked);
                    setToken("");
                  }}
                />
                {translate("Читать публичный репозиторий без токена")}
              </label>
              {!anonymous && (
                <p className="text-sm text-muted">
                  {translate(
                    "Fine-grained token: Contents, Pull requests и Actions — Read-only. Metadata предоставляется автоматически.",
                  )}
                </p>
              )}
            </>
          ) : (
            <p className="text-sm text-muted leading-relaxed">
              {translate(
                "Создайте в GitLab токен с ограниченным сроком и правами",
              )}
              <code> read_api</code> {translate("и")}{" "}
              <code>read_repository</code>{" "}
              {translate(
                "для этого проекта. Подойдёт Project Access Token с ролью Reporter или Personal Access Token участника проекта. Для синхронизации достаточно режима «Только API».",
              )}
            </p>
          )}
          {(!github || !anonymous) && (
            <label>
              {translate("API-ключ {0}", label)}
              <input
                type="password"
                required
                minLength={8}
                maxLength={4096}
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
            </label>
          )}
          {!github && (
            <label>
              {translate("Режим подключения GitLab")}
              <select
                value={mode}
                onChange={(e) => {
                  setMode(e.target.value as ForgeStatus["webhook_mode"]);
                  setWebhook("");
                }}
              >
                <option value="disabled">
                  {translate("Только API, без вебхуков")}
                </option>
                <option value="signature">{translate("Подпись HMAC")}</option>
                <option value="legacy">
                  {translate("Секретный заголовок GitLab")}
                </option>
              </select>
            </label>
          )}
          {!github && mode !== "disabled" && (
            <label>
              {mode === "signature"
                ? "Signing token GitLab"
                : translate("Секрет вебхука GitLab")}
              <input
                type="password"
                required
                autoComplete="off"
                minLength={mode === "signature" ? 8 : 16}
                maxLength={256}
                placeholder={
                  mode === "signature"
                    ? "whsec_…"
                    : translate("Не менее 16 символов")
                }
                value={webhook}
                onChange={(e) => setWebhook(e.target.value)}
              />
            </label>
          )}
          <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
            {github
              ? translate(
                  "Без токена действует небольшой общий лимит GitHub API. При его достижении очередь сохранится и сверка продолжится позже.",
                )
              : mode === "disabled"
                ? translate(
                    "Синхронизация работает автоматически. Вебхуки ускоряют обновления.",
                  )
                : translate(
                    "Возьмите ключ подписи из настроек вебхука GitLab. Для старой схемы задайте одинаковый секрет в GitLab и здесь.",
                  )}{" "}
            {translate(
              "Поля очищаются после отправки; повторная настройка заменяет ключи и режим подключения.",
            )}
          </p>
          <button className={buttonStyles.primary} disabled={action.pending}>
            {translate("Проверить и сохранить {0}", label)}
          </button>
        </MutationForm>
      )}
      {canManage && removing && (
        <ConfirmAction
          title={translate("Отключить {0}?", label)}
          pending={action.pending}
          error={action.error}
          onClose={() => setRemoving(false)}
          onConfirm={() => void remove()}
          label={translate("Отключить {0}", label)}
        >
          <p>
            {translate(
              "Ключи будут удалены из Gitmora. Новые сверки и вебхуки этого соединения перестанут обрабатываться. Уже отправленный внешний запрос может завершиться.",
            )}
          </p>
        </ConfirmAction>
      )}
    </div>
  );
}
