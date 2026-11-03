import { t as translate } from "../../domain/locale.js";
import { useState, useEffect } from "react";
import type { FormEvent } from "react";
import { repositoryURL } from "../../domain/context.js";
import { RepositoryAccess } from "./RepositoryAccess.js";
import {
  clearRepositoryToken,
  repositoryAccessBody,
  type RepositoryAccessValue,
} from "../../domain/repository-access.js";
import type { Repository } from "../../domain/context.js";
import type { ForgeStatus } from "../../domain/settings.js";
import { integrationErrors } from "../../transport/api.js";
import type { ApiClient } from "../../transport/api.js";
import { useMutation } from "../../application/useMutation.js";
import { useConnection } from "./useConnection.js";
import {
  ConfirmAction,
  MutationForm,
  MutationNotice,
} from "../SettingsShared.js";
import { ErrorNotice, date } from "../shared.js";
import { buttonStyles, sharedStyles } from "../ui.js";

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
  const path = `/v1/repositories/${repository.id}/forge`;
  const status = useConnection<ForgeStatus>(client, path, canRead);
  const action = useMutation(client);
  const [editing, setEditing] = useState(false);
  const [accessRevision, setAccessRevision] = useState(0);
  const [removing, setRemoving] = useState(false);
  const [access, setAccess] = useState<RepositoryAccessValue>({
    kind: "new",
    token: "",
  });
  const [mode, setMode] = useState<ForgeStatus["webhook_mode"]>("disabled");
  const [webhook, setWebhook] = useState("");
  useEffect(() => {
    if (!canManage) {
      setEditing(false);
      setAccess({ kind: "new", token: "" });
      setWebhook("");
      setRemoving(false);
    }
  }, [canManage]);
  const value = status.value;
  const issue = value?.last_error_code;
  function open(withToken = false) {
    setWebhook("");
    setAccess(
      !withToken && value?.anonymous
        ? { kind: "public" }
        : { kind: "new", token: "" },
    );
    setMode(value?.webhook_mode ?? "disabled");
    setEditing(true);
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    const body = {
      ...repositoryAccessBody(access),
      webhook_mode: github ? "disabled" : mode,
      ...(mode === "signature"
        ? { webhook_signing_token: webhook }
        : mode === "legacy"
          ? { legacy_webhook_token: webhook }
          : {}),
    };
    setAccess(clearRepositoryToken(access));
    setWebhook("");
    const saved = await action.run<ForgeStatus>("PUT", path, body);
    if (saved) {
      status.setValue(saved.value);
      setEditing(false);
      action.setMessage("Подключение сохранено.");
    } else if (access.kind === "saved") setAccessRevision((value) => value + 1);
  }
  async function sync() {
    if (await action.run("POST", path + "/sync")) {
      action.setMessage("Синхронизация запущена.");
      await status.refresh();
    }
  }
  async function remove() {
    if (await action.run("DELETE", path)) {
      setRemoving(false);
      setEditing(false);
      await status.refresh();
    }
  }
  const form = canManage && (editing || status.missing);
  return (
    <div className="connection-detail">
      <ErrorNotice>{status.error}</ErrorNotice>
      {status.loading && !value && (
        <p role="status">{translate("Загрузка…")}</p>
      )}
      {value && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
            <span
              className={`text-sm font-medium ${!value.enabled ? "text-muted" : issue || value.blocked ? "text-danger" : "text-success"}`}
            >
              {!value.enabled
                ? translate("Отключено")
                : issue || value.blocked
                  ? translate("Требует внимания")
                  : translate("Подключено")}
            </span>
            {value.last_synced_at && (
              <span className="text-xs text-muted">
                {translate("Обновлено")} {date(value.last_synced_at)}
              </span>
            )}
          </div>
          {issue && (
            <div
              className="mb-4 border-l-2 border-danger bg-danger-soft px-4 py-3 text-sm text-danger"
              role="status"
            >
              <p className="mb-0">
                {github && value.anonymous && issue === "provider_rate_limited"
                  ? translate(
                      "Достигнут общий лимит GitHub. Подключите токен чтения для регулярной синхронизации.",
                    )
                  : translate(
                      integrationErrors[issue] ??
                        "Синхронизация недоступна. Проверьте подключение.",
                    )}
              </p>
              {value.next_sync_at && (
                <p className="mt-2 mb-0 text-xs">
                  {translate("Следующая попытка")} {date(value.next_sync_at)}
                </p>
              )}
            </div>
          )}
        </>
      )}
      <MutationNotice action={action} />
      {!form && canManage && (
        <div className="flex flex-wrap gap-3">
          <button
            className={buttonStyles.secondary}
            onClick={() => open(!!issue && github)}
          >
            {issue && github
              ? translate("Подключить токен")
              : translate("Изменить доступ")}
          </button>
          {value?.enabled && (
            <button
              className={buttonStyles.secondary}
              disabled={action.pending || issue === "provider_rate_limited"}
              onClick={() => void sync()}
            >
              {translate("Синхронизировать")}
            </button>
          )}
        </div>
      )}
      {form && (
        <MutationForm
          pending={action.pending}
          onSubmit={(e) => void save(e)}
          className="grid gap-4"
        >
          <RepositoryAccess
            client={client}
            projectID={repository.project_id}
            provider={github ? "github" : "gitlab"}
            url={repositoryURL(repository)}
            repositoryID={repository.id}
            value={access}
            onChange={setAccess}
            refreshKey={accessRevision}
          />
          {!github && (
            <details className="text-sm">
              <summary>{translate("Вебхуки")}</summary>
              <label className="mt-3">
                {translate("Способ проверки")}
                <select
                  className="block mt-2 w-full"
                  value={mode}
                  onChange={(e) =>
                    setMode(e.target.value as ForgeStatus["webhook_mode"])
                  }
                >
                  <option value="disabled">{translate("Только API")}</option>
                  <option value="signature">HMAC</option>
                  <option value="legacy">
                    {translate("Секретный заголовок")}
                  </option>
                </select>
              </label>
              {mode !== "disabled" && (
                <label className="mt-3">
                  {translate("Секрет вебхука")}
                  <input
                    className="block mt-2 w-full"
                    type="password"
                    required
                    value={webhook}
                    autoComplete="off"
                    minLength={8}
                    maxLength={4096}
                    onChange={(e) => setWebhook(e.target.value)}
                  />
                </label>
              )}
            </details>
          )}
          <div className="flex gap-3">
            <button className={buttonStyles.primary}>
              {action.pending
                ? translate("Проверяем…")
                : translate("Сохранить подключение")}
            </button>
            {!status.missing && (
              <button
                type="button"
                className={buttonStyles.secondary}
                onClick={() => {
                  setEditing(false);
                  setAccess({ kind: "new", token: "" });
                  setWebhook("");
                }}
              >
                {translate("Отмена")}
              </button>
            )}
          </div>
        </MutationForm>
      )}
      {value && (
        <details className="mt-5 border-t border-line pt-4 text-sm">
          <summary>{translate("Состояние синхронизации")}</summary>
          <dl className={sharedStyles.connectionScope}>
            <dt>{translate("Следующая попытка")}</dt>
            <dd>{date(value.next_sync_at)}</dd>
            {value.webhook_mode !== "disabled" && (
              <>
                <dt>{translate("Адрес вебхука")}</dt>
                <dd className="break-all">
                  {location.origin + value.webhook_path}
                </dd>
              </>
            )}
          </dl>
          <button
            className={buttonStyles.text}
            disabled={status.loading}
            onClick={() => void status.refresh()}
          >
            {translate("Обновить состояние")}
          </button>
          {canManage && value.enabled && (
            <button
              className={sharedStyles.destructiveText + " ml-5"}
              onClick={() => setRemoving(true)}
            >
              {translate("Отключить {0}", label)}
            </button>
          )}
        </details>
      )}
      {removing && (
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
              "Новые данные перестанут поступать. Сохранённые сессии останутся в Gitmora.",
            )}
          </p>
        </ConfirmAction>
      )}
    </div>
  );
}
