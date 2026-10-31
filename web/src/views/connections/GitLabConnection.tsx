import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Repository } from "../../domain/context.js";
import type { GitLabStatus } from "../../domain/settings.js";
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

export function GitLabConnection({
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
  const path = `/v1/repositories/${repository.id}/gitlab`;
  const status = useConnection<GitLabStatus>(client, path, canRead);
  const action = useMutation(client);
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [token, setToken] = useState("");
  const [webhook, setWebhook] = useState("");
  const [mode, setMode] = useState<GitLabStatus["webhook_mode"]>("disabled");
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
      api_token: token,
      webhook_mode: mode,
      ...(mode === "signature"
        ? { webhook_signing_token: webhook }
        : mode === "legacy"
          ? { legacy_webhook_token: webhook }
          : {}),
    };
    setToken("");
    setWebhook("");
    const saved = await action.run<GitLabStatus>("PUT", path, body);
    if (saved) {
      status.setValue(saved.value);
      setEditing(false);
      action.setMessage("Соединение GitLab сохранено после проверки доступа.");
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
      action.setMessage(
        "GitLab отключён. Сохранённые ключи соединения удалены.",
      );
      await status.refresh();
    }
  }
  const value = status.value;
  return (
    <div className="connection-detail mt-6">
      <dl className={sharedStyles.connectionScope}>
        <dt>{translate("Сервер")}</dt>
        <dd>{repository.gitlab_url}</dd>
        <dt>{translate("Проект GitLab")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {providerLabel(repository.gitlab_project_id)}
        </dd>
      </dl>
      <ErrorNotice>{status.error}</ErrorNotice>
      {status.loading && (
        <p role="status">{translate("Читаем состояние GitLab…")}</p>
      )}
      {status.missing && (
        <p className="muted text-muted">
          {translate("Соединение GitLab ещё не настроено.")}
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
              aria-label={translate("Потоки синхронизации GitLab")}
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
            {translate("Обновить состояние GitLab")}
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
              }}
            >
              {editing
                ? translate("Отменить настройку")
                : value?.enabled
                  ? translate("Заменить ключи GitLab")
                  : translate("Настроить GitLab")}
            </button>
            {value?.enabled && (
              <>
                <button
                  className={buttonStyles.secondary}
                  disabled={action.pending}
                  onClick={() => void sync()}
                >
                  {translate("Запросить сверку GitLab")}
                </button>
                <button
                  className={sharedStyles.destructiveText}
                  onClick={() => setRemoving(true)}
                >
                  {translate("Отключить GitLab")}
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
          <h3>{translate("Доступ к GitLab")}</h3>
          <p className="text-sm text-muted leading-relaxed">
            {translate(
              "Создайте в GitLab токен с ограниченным сроком и правами",
            )}
            <code> read_api</code> {translate("и")} <code>read_repository</code>{" "}
            {translate(
              "для этого проекта. Подойдёт Project Access Token с ролью Reporter или Personal Access Token участника проекта. Для синхронизации достаточно режима «Только API».",
            )}
          </p>
          <label>
            {translate("API-ключ GitLab")}
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
          <label>
            {translate("Режим подключения GitLab")}
            <select
              value={mode}
              onChange={(e) => {
                setMode(e.target.value as GitLabStatus["webhook_mode"]);
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
          {mode !== "disabled" && (
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
            {mode === "disabled"
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
            {translate("Проверить и сохранить GitLab")}
          </button>
        </MutationForm>
      )}
      {canManage && removing && (
        <ConfirmAction
          title={translate("Отключить GitLab?")}
          pending={action.pending}
          error={action.error}
          onClose={() => setRemoving(false)}
          onConfirm={() => void remove()}
          label={translate("Отключить GitLab")}
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
