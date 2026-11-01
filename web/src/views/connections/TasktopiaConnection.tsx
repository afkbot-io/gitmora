import { t as translate } from "../../domain/locale.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Project } from "../../domain/context.js";
import type { TasktopiaStatus } from "../../domain/settings.js";
import type { ApiClient } from "../../transport/api.js";
import { useMutation } from "../../application/useMutation.js";
import {
  ConfirmAction,
  MutationNotice,
  MutationForm,
} from "../SettingsShared.js";
import { ErrorNotice } from "../shared.js";
import { useConnection } from "./useConnection.js";
import { buttonStyles, sharedStyles } from "../ui.js";
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
  const value = status.value;
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [endpoint, setEndpoint] = useState("");
  const [country, setCountry] = useState("");
  const [token, setToken] = useState("");
  const [autoLink, setAutoLink] = useState(false);
  useEffect(() => {
    if (!canManage) {
      setEditing(false);
      setToken("");
      setRemoving(false);
    }
  }, [canManage]);
  function open() {
    setEndpoint(value?.endpoint ?? "");
    setCountry(value?.country_id ?? "");
    setAutoLink(value?.auto_link_merge_requests ?? false);
    setToken("");
    setEditing(true);
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    let target = endpoint.trim();
    try {
      const u = new URL(target);
      if (u.pathname === "/" || u.pathname === "") target = u.origin + "/mcp";
    } catch {
      /* Validated by browser and server. */
    }
    const body = {
      endpoint: target,
      country_id: country.trim(),
      api_token: token,
      auto_link_merge_requests: autoLink,
    };
    setToken("");
    const saved = await action.run<TasktopiaStatus>("PUT", path, body);
    if (saved) {
      status.setValue(saved.value);
      setEditing(false);
      action.setMessage("Tasktopia подключена.");
    }
  }
  async function remove() {
    if (await action.run("DELETE", path)) {
      setRemoving(false);
      setEditing(false);
      await status.refresh();
    }
  }
  return (
    <section className="grid grid-cols-[220px_minmax(0,1fr)] gap-6 border-b border-line pb-8 mobile:grid-cols-1">
      <div>
        <h2 className="text-base">Tasktopia</h2>
        <p className="text-sm text-muted">
          {translate("Задачи и связи с изменениями кода.")}
        </p>
      </div>
      <div className="min-w-0">
        <ErrorNotice>{editing ? status.error : ""}</ErrorNotice>
        <MutationNotice action={action} />
        {value && (
          <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
            <strong
              className={`text-sm ${value.enabled && !value.blocked && !value.error_code ? "text-success" : "text-muted"}`}
            >
              {value.enabled
                ? value.blocked || value.error_code
                  ? translate("Требует внимания")
                  : translate("Подключено")
                : translate("Отключено")}
            </strong>
            <span className="text-sm text-muted break-all">
              {value.endpoint}
            </span>
          </div>
        )}
        {!editing && canManage && (
          <button className={buttonStyles.secondary} onClick={open}>
            {value
              ? translate("Изменить доступ")
              : translate("Подключить Tasktopia")}
          </button>
        )}
        {editing && (
          <MutationForm
            pending={action.pending}
            onSubmit={(e) => void save(e)}
            className="grid gap-4"
          >
            <label>
              {translate("URL Tasktopia")}
              <input
                className="w-full mt-2"
                name="tasktopia_url"
                type="url"
                required
                readOnly={!!value}
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder="https://tasktopia.company.ru/mcp"
              />
            </label>
            <label>
              {translate("UUID пространства Tasktopia")}
              <input
                className="w-full mt-2"
                name="tasktopia_space"
                required
                readOnly={!!value}
                pattern="[0-9a-fA-F-]{36}"
                value={country}
                onChange={(e) => setCountry(e.target.value)}
              />
            </label>
            <label>
              {translate("Ключ Tasktopia")}
              <input
                className="w-full mt-2"
                name="tasktopia_token"
                type="password"
                required
                autoComplete="off"
                minLength={8}
                maxLength={4096}
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
            </label>
            <label className="flex items-center gap-2 text-sm font-normal">
              <input
                type="checkbox"
                checked={autoLink}
                onChange={(e) => setAutoLink(e.target.checked)}
              />
              {translate("Добавлять ссылки на запросы слияния в задачи")}
            </label>
            <details className="text-sm text-muted">
              <summary>{translate("Где найти ключ и UUID")}</summary>
              <p className="mt-3">
                {translate(
                  "В Tasktopia откройте настройки → MCP-интеграция и создайте ключ с country:read и tasks:read. UUID страны можно получить через MCP country.list. Для добавления ссылок нужно tasks:write.",
                )}
              </p>
            </details>
            <div className="flex flex-wrap gap-3">
              <button className={buttonStyles.primary}>
                {translate("Сохранить подключение")}
              </button>
              <button
                type="button"
                className={buttonStyles.secondary}
                onClick={() => {
                  setEditing(false);
                  setToken("");
                }}
              >
                {translate("Отмена")}
              </button>
            </div>
          </MutationForm>
        )}
        {value && (
          <details className="mt-5 border-t border-line pt-4 text-sm">
            <summary>{translate("Состояние подключения")}</summary>
            <dl className={sharedStyles.connectionScope}>
              <dt>{translate("Пространство")}</dt>
              <dd className="break-all">{value.country_id}</dd>
              <dt>{translate("Статус")}</dt>
              <dd>
                {value.error_code
                  ? translate(
                      "Не удалось обновить задачи. Проверьте адрес и права ключа.",
                    )
                  : translate("Ошибок нет")}
              </dd>
            </dl>
            <button
              className={buttonStyles.text}
              onClick={() => void status.refresh()}
            >
              {translate("Обновить состояние")}
            </button>
            {canManage && value.enabled && (
              <button
                className={sharedStyles.destructiveText + " ml-5"}
                onClick={() => setRemoving(true)}
              >
                {translate("Отключить Tasktopia")}
              </button>
            )}
          </details>
        )}
        {removing && (
          <ConfirmAction
            title={translate("Отключить Tasktopia?")}
            pending={action.pending}
            error={action.error}
            onClose={() => setRemoving(false)}
            onConfirm={() => void remove()}
            label={translate("Отключить Tasktopia")}
          >
            <p>{translate("Новые задачи перестанут поступать в Gitmora.")}</p>
          </ConfirmAction>
        )}
      </div>
    </section>
  );
}
