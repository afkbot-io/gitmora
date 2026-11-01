import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useState } from "react";
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
import { AgentSetup } from "./AgentSetup.js";

export function RepositoryCatalog({
  client,
  me,
  project,
  repositories,
  onSaved,
}: {
  client: ApiClient;
  me: Me;
  project: Project;
  repositories: Repository[];
  onSaved: () => Promise<void>;
}) {
  const canConnect =
    capabilities(me, project).catalog && capabilities(me, project).integrations;
  const [open, setOpen] = useState(repositories.length === 0 && canConnect);
  return (
    <section className="repository-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1>{translate("Репозитории")}</h1>
        </div>
        {canConnect && (
          <button
            className={buttonStyles.primary}
            onClick={() => setOpen(!open)}
          >
            {open
              ? translate("Скрыть форму")
              : translate("Добавить репозиторий")}
          </button>
        )}
      </div>
      {canConnect && open && (
        <RepositoryCreate
          client={client}
          project={project}
          onSaved={async () => {
            await onSaved();
            setOpen(false);
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
}: {
  client: ApiClient;
  project: Project;
  onSaved: () => Promise<void>;
}) {
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
      if (saved) await onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : translate("Проверьте данные."));
    }
  }
  return (
    <MutationForm
      pending={action.pending || lookup.pending || connection.pending}
      className={sharedStyles.setup}
      onSubmit={(event) => void create(event)}
    >
      <h2>{translate("Подключить репозиторий")}</h2>
      <div className="grid grid-cols-[220px_minmax(0,1fr)] gap-5 mobile:grid-cols-1">
        <label>
          {translate("Сервис")}
          <select
            aria-label={translate("Сервис")}
            value={host}
            disabled={created}
            onChange={(e) => {
              setHost(e.target.value as "github" | "gitlab");
              setResolved(null);
              setURL("");
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
              setURL(e.target.value);
              setResolved(null);
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
        {action.pending || lookup.pending || connection.pending
          ? translate("Подключаем…")
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
  const path = `/v1/projects/${project.id}/repositories/${repository.id}`;
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
  const origin = repositoryURL(repository);
  const url = safeExternalURL(origin);
  return (
    <article
      className={`
      settings-resource border-b border-line px-0 py-4 [&_.repository-row]:border-0
      [&_.repository-row]:px-0 [&_.repository-row]:py-4 [&_.repository-row_>_div:first-child]:[flex:1]
    `}
    >
      <div
        className={`
        repository-row px-0 py-6 border-t border-line flex items-center justify-between gap-5
        flex-wrap [&_h2]:mb-1 [&_p]:m-0 [&_p]:text-sm [&_dt]:text-xs [&_dt]:text-muted [&_dt]:mb-1
        [&_>_span]:text-xs mobile:gap-3 mobile:[&_>_span]:w-full
        mobile:[&_a]:wrap-anywhere
      `}
      >
        <div>
          <h2>{repository.name}</h2>
          <p>
            {url ? (
              <a href={url} target="_blank" rel="noopener noreferrer">
                {origin}
              </a>
            ) : (
              translate("Адрес репозитория недоступен")
            )}
          </p>
        </div>
        <span className="text-xs text-muted">
          {repository.provider === "github" ? "GitHub" : "GitLab"}
        </span>
        {capabilities(me, project).catalog && (
          <div className="actions flex gap-3 flex-wrap items-center">
            <button
              className={buttonStyles.secondary}
              onClick={() => setEditing(!editing)}
            >
              {translate("Переименовать")}
            </button>
            <button
              className={sharedStyles.destructiveText}
              onClick={() => setDeleting(true)}
            >
              {translate("Удалить репозиторий")}
            </button>
          </div>
        )}
      </div>
      {project.role && <AgentSetup repositoryID={repository.id} />}
      {capabilities(me, project).catalog && editing && (
        <Rename
          key={repository.version}
          client={client}
          path={path}
          name={repository.name}
          version={repository.version}
          onSaved={onSaved}
          label={translate("Новое название репозитория")}
        />
      )}
      {capabilities(me, project).catalog && deleting && (
        <ConfirmAction
          title={translate("Удалить {0}?", repository.name)}
          pending={action.pending}
          error={action.error}
          onClose={() => setDeleting(false)}
          onConfirm={() => void remove()}
          label={translate("Удалить локальные данные")}
        >
          <p>
            {translate(
              "Будут удалены запись репозитория и связанные локальные сессии, индексы и результаты. Исходный репозиторий останется у вашего провайдера.",
            )}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
