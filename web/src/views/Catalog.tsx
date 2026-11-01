import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useRef, useState } from "react";
import type { FormEvent } from "react";
import type {
  Me,
  Project,
  Repository,
  ResolvedRepository,
} from "../domain/context.js";
import { safeExternalURL, repositoryURL } from "../domain/context.js";
import { capabilities } from "../domain/settings.js";
import type { ApiClient } from "../transport/api.js";
import { useMutation } from "../application/useMutation.js";
import {
  ConfirmAction,
  MutationNotice,
  Rename,
  MutationForm,
} from "./SettingsShared.js";
import { Empty, ErrorNotice } from "./shared.js";
import { ForgeConnection } from "./connections/ForgeConnection.js";

export function RepositoryCatalog({
  client,
  me,
  project,
  repositories,
  onSaved,
  onConnected,
}: {
  client: ApiClient;
  me: Me;
  project: Project;
  repositories: Repository[];
  onSaved: () => Promise<void>;
  onConnected: (id: string) => void;
}) {
  const canConnect =
    capabilities(me, project).catalog && capabilities(me, project).integrations;
  const [open, setOpen] = useState(repositories.length === 0 && canConnect);
  return (
    <section className="repository-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1 className="mb-2">
            {translate(
              repositories.length
                ? "Репозитории"
                : "Подключите первый репозиторий",
            )}
          </h1>
          {!repositories.length && (
            <p className="mb-0 text-sm text-muted">
              {translate(
                "Вставьте ссылку на репозиторий. Адрес сервера и ID определим автоматически.",
              )}
            </p>
          )}
        </div>
        {canConnect && repositories.length > 0 && (
          <button
            className={buttonStyles.primary}
            onClick={() => setOpen(!open)}
          >
            {open ? translate("Отмена") : translate("Добавить репозиторий")}
          </button>
        )}
      </div>
      {canConnect && open && (
        <RepositoryCreate
          client={client}
          project={project}
          onRegistered={onSaved}
          onSaved={async (id) => {
            await onSaved();
            setOpen(false);
            onConnected(id);
          }}
        />
      )}
      {repositories.length === 0 && !(open && canConnect) && (
        <Empty title={translate("Подключите первый репозиторий")}>
          {translate(
            "Добавьте GitHub или GitLab, чтобы сохранять историю разработки.",
          )}
        </Empty>
      )}
      {repositories.map((repository) => (
        <RepositoryEntry
          key={repository.id}
          client={client}
          me={me}
          project={project}
          repository={repository}
          onSaved={onSaved}
        />
      ))}
    </section>
  );
}
function RepositoryCreate({
  client,
  project,
  onSaved,
  onRegistered,
}: {
  client: ApiClient;
  project: Project;
  onSaved: (id: string) => Promise<void>;
  onRegistered: () => Promise<void>;
}) {
  const busy = useRef(false);
  const [saving, setSaving] = useState(false);
  const [host, setHost] = useState<"github" | "gitlab">("github");
  const [url, setURL] = useState("");
  const [token, setToken] = useState("");
  const [name, setName] = useState("");
  const [anonymous, setAnonymous] = useState(false);
  const [id] = useState(() => crypto.randomUUID());
  const [created, setCreated] = useState(false);
  const [resolved, setResolved] = useState<ResolvedRepository | null>(null);
  const action = useMutation(client);
  const lookup = useMutation(client);
  const connection = useMutation(client);
  const [error, setError] = useState("");
  async function create(event: FormEvent) {
    event.preventDefault();
    if (busy.current) return;
    busy.current = true;
    setSaving(true);
    setError("");
    const credential = token;
    setToken("");
    let identity = resolved;
    try {
      if (!created) {
        const found = await lookup.run<ResolvedRepository>(
          "POST",
          `/v1/projects/${project.id}/repositories/lookup`,
          {
            url: url.trim(),
            provider: host,
            ...(!anonymous || host !== "github"
              ? { api_token: credential }
              : {}),
          },
        );
        if (!found) return;
        identity = found.value;
        if (
          identity.provider !== (host === "github" ? "github" : "") ||
          !Number.isSafeInteger(identity.provider_repository_id) ||
          identity.provider_repository_id < 1
        )
          throw new Error(translate("Проверьте данные репозитория."));
        setResolved(identity);
        const fields =
          host === "github"
            ? {
                provider: "github",
                provider_url: identity.provider_url,
                provider_repository_id: identity.provider_repository_id,
              }
            : {
                gitlab_url: identity.provider_url,
                gitlab_project_id: identity.provider_repository_id,
              };
        const saved = await action.run(
          "POST",
          `/v1/projects/${project.id}/repositories`,
          { id, name: name.trim() || identity.name, ...fields },
          true,
        );
        if (!saved) return;
        setCreated(true);
        await onRegistered();
      }
      const saved = await connection.run(
        "PUT",
        `/v1/repositories/${id}/forge`,
        {
          ...(host === "github" && anonymous
            ? { anonymous: true }
            : { api_token: credential }),
          webhook_mode: "disabled",
        },
      );
      if (saved) await onSaved(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : translate("Проверьте данные."));
    } finally {
      busy.current = false;
      setSaving(false);
    }
  }
  return (
    <MutationForm
      pending={saving}
      className={sharedStyles.setup}
      onSubmit={(event) => void create(event)}
    >
      <div className="grid grid-cols-[160px_minmax(0,1fr)] gap-4 mobile:grid-cols-1">
        <label>
          {translate("Сервис")}
          <select
            aria-label={translate("Сервис")}
            value={host}
            disabled={created}
            onChange={(e) => {
              setHost(e.target.value as "github" | "gitlab");
              setResolved(null);
              setAnonymous(false);
              setToken("");
            }}
          >
            <option value="github">GitHub</option>
            <option value="gitlab">GitLab</option>
          </select>
        </label>
        <label>
          {translate("Ссылка на репозиторий")}
          <input
            type="url"
            name="repository_url"
            required
            readOnly={created}
            value={url}
            onChange={(e) => {
              const value = e.target.value;
              setURL(value);
              setResolved(null);
              try {
                const origin = new URL(value).hostname.toLowerCase();
                if (origin === "github.com") setHost("github");
                else if (origin === "gitlab.com") setHost("gitlab");
              } catch {
                /* The form validates the final URL. */
              }
            }}
            placeholder={
              host === "github"
                ? "https://github.com/team/project"
                : "https://gitlab.company.ru/team/project"
            }
          />
        </label>
      </div>
      {!(host === "github" && anonymous) && (
        <label>
          {translate("Токен чтения")}
          <input
            type="password"
            name="repository_token"
            required
            autoComplete="off"
            minLength={8}
            maxLength={4096}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>
      )}
      <details className="mb-4 text-sm text-muted">
        <summary>{translate("Как получить токен")}</summary>
        <p className="mt-3">
          {host === "github"
            ? translate(
                "GitHub → Settings → Developer settings → Fine-grained tokens. Выберите репозиторий и разрешите Contents, Pull requests и Actions: Read-only.",
              )
            : translate(
                "GitLab → Settings → Access tokens. Создайте токен с правом read_api и доступом к этому проекту.",
              )}
        </p>
        <a
          className="inline-flex py-2"
          target="_blank"
          rel="noopener noreferrer"
          href={
            host === "github"
              ? "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens"
              : "https://docs.gitlab.com/user/profile/personal_access_tokens/"
          }
        >
          {translate("Инструкция сервиса")}
        </a>
      </details>
      {host === "github" && (
        <label className="checkbox">
          <input
            type="checkbox"
            checked={anonymous}
            onChange={(e) => {
              setAnonymous(e.target.checked);
              setToken("");
            }}
          />
          {translate("Публичный репозиторий без токена")}
        </label>
      )}
      {host === "github" && anonymous && (
        <p className="text-sm text-muted">
          {translate("Без токена доступен небольшой общий лимит GitHub API.")}
        </p>
      )}
      <details className="mb-5 text-sm">
        <summary>{translate("Дополнительно")}</summary>
        <label className="mt-4">
          {translate("Название в Gitmora")}
          <input
            value={name}
            maxLength={200}
            readOnly={created}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
      </details>
      <ErrorNotice>{error}</ErrorNotice>
      <MutationNotice action={lookup} />
      <MutationNotice action={action} />
      <MutationNotice action={connection} />
      {created && connection.error && (
        <p className="text-sm text-muted">
          {translate(
            "Репозиторий добавлен. Введите действующий токен, чтобы завершить подключение.",
          )}
        </p>
      )}
      <button className={buttonStyles.primary}>
        {saving
          ? translate("Подключаем…")
          : created
            ? translate("Сохранить подключение")
            : translate("Подключить репозиторий")}
      </button>
    </MutationForm>
  );
}
function RepositoryEntry({
  client,
  me,
  project,
  repository,
  onSaved,
}: {
  client: ApiClient;
  me: Me;
  project: Project;
  repository: Repository;
  onSaved: () => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const action = useMutation(client);
  const rights = capabilities(me, project);
  const path = `/v1/projects/${project.id}/repositories/${repository.id}`;
  const origin = repositoryURL(repository);
  const url = safeExternalURL(origin);
  async function remove() {
    if (
      await action.run(
        "DELETE",
        path,
        { expected_version: repository.version },
        true,
      )
    ) {
      setDeleting(false);
      await onSaved();
    }
  }
  return (
    <article className="settings-resource mt-4 overflow-hidden rounded-lg border border-line bg-paper">
      <div className="repository-row flex flex-wrap items-center justify-between gap-4 p-4">
        <div className="min-w-0 flex-1">
          <h2 className="mb-1 text-base">{repository.name}</h2>
          <p className="m-0 truncate text-xs text-muted">
            {repository.provider === "github" ? "GitHub" : "GitLab"} ·{" "}
            {url ? (
              <a href={url} target="_blank" rel="noopener noreferrer">
                {origin}
              </a>
            ) : (
              origin
            )}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <a
            className={buttonStyles.secondary + " action-link text-sm"}
            href={`#view=setup&project=${project.id}&repository=${repository.id}`}
          >
            {translate("CLI и MCP")}
          </a>
          <button
            className={buttonStyles.secondary + " text-sm"}
            aria-expanded={editing}
            onClick={() => setEditing(!editing)}
          >
            {translate(editing ? "Свернуть" : "Настроить")}
          </button>
        </div>
      </div>
      {editing && (
        <div className="border-t border-line p-4">
          <ForgeConnection
            client={client}
            repository={repository}
            canRead={rights.integrationRead}
            canManage={rights.integrations}
          />
          {rights.catalog && (
            <details className="mt-5 border-t border-line pt-3 text-sm">
              <summary className="py-2">
                {translate("Название и удаление")}
              </summary>
              <Rename
                key={repository.version}
                client={client}
                path={path}
                name={repository.name}
                version={repository.version}
                onSaved={onSaved}
                label={translate("Новое название репозитория")}
              />
              <button
                className={sharedStyles.destructiveText}
                onClick={() => setDeleting(true)}
              >
                {translate("Удалить репозиторий")}
              </button>
            </details>
          )}
        </div>
      )}
      {deleting && (
        <ConfirmAction
          title={translate("Удалить {0}?", repository.name)}
          pending={action.pending}
          error={action.error}
          onClose={() => setDeleting(false)}
          onConfirm={() => void remove()}
          label={translate("Удалить репозиторий и его данные")}
        >
          <p>
            {translate(
              "Сессии и история этого репозитория будут удалены из Gitmora.",
            )}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
