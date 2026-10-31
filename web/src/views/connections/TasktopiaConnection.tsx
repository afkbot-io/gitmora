import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Project } from "../../domain/context.js";
import type { TasktopiaStatus } from "../../domain/settings.js";
import { diagnostic } from "../../domain/settings.js";
import { useMutation } from "../../application/useMutation.js";
import type { ApiClient } from "../../transport/api.js";
import {
  ConfirmAction,
  MutationNotice,
  MutationForm,
} from "../SettingsShared.js";
import { ErrorNotice } from "../shared.js";
import { useConnection } from "./useConnection.js";

export function TasktopiaConnection({
  client,
  project,
  canRead,
  canManage,
}: {
  client: ApiClient;
  project: Project;
  canRead: boolean;
  canManage: boolean;
}) {
  const path = `/v1/projects/${project.id}/tasktopia`;
  const status = useConnection<TasktopiaStatus>(client, path, canRead);
  const action = useMutation(client);
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [endpoint, setEndpoint] = useState("");
  const [country, setCountry] = useState("");
  const [token, setToken] = useState("");
  const [autoLink, setAutoLink] = useState(false);
  const [task, setTask] = useState("");
  useEffect(() => {
    if (!canManage) {
      setEditing(false);
      setToken("");
      setRemoving(false);
    }
  }, [canManage]);
  function open() {
    setEditing(!editing);
    setToken("");
    setEndpoint(status.value?.endpoint ?? "");
    setCountry(status.value?.country_id ?? "");
    setAutoLink(status.value?.auto_link_merge_requests ?? false);
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    const body = {
      endpoint: endpoint.trim(),
      country_id: country.trim(),
      api_token: token,
      auto_link_merge_requests: autoLink,
    };
    setToken("");
    const saved = await action.run<TasktopiaStatus>("PUT", path, body);
    if (saved) {
      status.setValue(saved.value);
      setEditing(false);
      action.setMessage(
        "Соединение Tasktopia сохранено после проверки страны.",
      );
    }
  }
  async function refreshTask(event: FormEvent) {
    event.preventDefault();
    if (await action.run("POST", path + `/tasks/${task.trim()}/sync`))
      action.setMessage(
        "Обновление задачи запрошено. Оно выполняется фоновым процессом.",
      );
  }
  async function remove() {
    if (await action.run("DELETE", path)) {
      setRemoving(false);
      status.clear();
      action.setMessage("Tasktopia отключена. Ключ соединения удалён.");
      await status.refresh();
    }
  }
  const value = status.value;
  return (
    <section className="settings-section border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0">
      <h2>Tasktopia</h2>
      <p className="muted text-muted">
        {translate(
          "Одна страна Tasktopia на проект Gitmora. Адрес и страна сохраняются при замене ключа.",
        )}
      </p>
      <ErrorNotice>{status.error}</ErrorNotice>
      {status.loading && (
        <p role="status">{translate("Читаем состояние Tasktopia…")}</p>
      )}
      {status.missing && (
        <p className="muted text-muted">
          {translate("Tasktopia ещё не подключена.")}
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
            <dt>{translate("Адрес MCP")}</dt>
            <dd>{value.endpoint}</dd>
            <dt>{translate("Страна")}</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {value.country_id}
            </dd>
            <dt>{translate("Добавление MR")}</dt>
            <dd>
              {value.auto_link_merge_requests
                ? translate("Включено для подтверждённых связей")
                : translate("Выключено")}
            </dd>
            <dt>{translate("Диагностика")}</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {diagnostic(value.error_code)}
            </dd>
          </dl>
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
            {translate("Обновить состояние Tasktopia")}
          </button>
        )}
        {canManage && (
          <>
            <button className={buttonStyles.secondary} onClick={open}>
              {editing
                ? translate("Отменить настройку")
                : value
                  ? translate("Изменить Tasktopia")
                  : translate("Настроить Tasktopia")}
            </button>
            {value?.enabled && (
              <button
                className={sharedStyles.destructiveText}
                onClick={() => setRemoving(true)}
              >
                {translate("Отключить Tasktopia")}
              </button>
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
          <h3>{translate("Доступ к Tasktopia")}</h3>
          <div className={sharedStyles.formGrid}>
            <label>
              HTTP MCP endpoint
              <input
                type="url"
                required
                maxLength={2000}
                readOnly={!!value}
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder="https://tasktopia.internal/mcp"
              />
            </label>
            <label>
              {translate("UUID страны Tasktopia")}
              <input
                required
                pattern="[0-9a-fA-F-]{36}"
                readOnly={!!value}
                value={country}
                onChange={(e) => setCountry(e.target.value)}
              />
            </label>
          </div>
          <label>
            {translate("MCP-ключ Tasktopia")}
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
          <label className="checkbox [&_input]:[accent-color:var(--color-accent)] [&_input]:w-4 [&_input]:min-h-4 [&_input]:m-0">
            <input
              type="checkbox"
              checked={autoLink}
              onChange={(e) => setAutoLink(e.target.checked)}
            />
            {translate("Добавлять подтверждённые MR в задачи автоматически")}
          </label>
          <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
            {translate(
              "Отдельному сервисному аккаунту нужны права чтения страны и задач. Добавление ссылок требует tasks:write. Эта политика не меняет стадию, приёмку или описание задачи. Ключ очищается после отправки; для изменения политики также требуется действующий ключ.",
            )}
          </p>
          <button className={buttonStyles.primary} disabled={action.pending}>
            {translate("Проверить и сохранить Tasktopia")}
          </button>
        </MutationForm>
      )}
      {canManage && value?.enabled && (
        <MutationForm
          pending={action.pending}
          className={`
            inline-form task-refresh [&_label]:flex [&_label]:flex-col [&_label]:gap-2 flex items-end gap-3
            flex-wrap px-0 py-4 [&_label]:[flex:1] [&_label]:min-w-50 [&_label]:max-w-125 [&_label]:mb-0
            [&_.notice]:w-full [&_.notice]:mb-0 [&_.error]:w-full [&_.error]:mb-0
          `}
          onSubmit={(event) => void refreshTask(event)}
        >
          <label>
            {translate("UUID задачи для обновления")}
            <input
              required
              pattern="[0-9a-fA-F-]{36}"
              value={task}
              onChange={(e) => setTask(e.target.value)}
            />
          </label>
          <button className={buttonStyles.secondary} disabled={action.pending}>
            {translate("Обновить задачу Tasktopia")}
          </button>
        </MutationForm>
      )}
      {canManage && removing && (
        <ConfirmAction
          title={translate("Отключить Tasktopia?")}
          pending={action.pending}
          error={action.error}
          onClose={() => setRemoving(false)}
          onConfirm={() => void remove()}
          label={translate("Отключить Tasktopia")}
        >
          <p>
            {translate(
              "Ключ будет удалён. Фоновый процесс перестанет получать новые задачи и публиковать данные. Уже отправленный внешний запрос может завершиться.",
            )}
          </p>
        </ConfirmAction>
      )}
    </section>
  );
}
