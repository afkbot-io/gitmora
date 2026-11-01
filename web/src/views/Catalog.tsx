import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useId, useState } from "react";
import type { FormEvent } from "react";
import type {
  Me,
  Project,
  Repository,
  ResolvedRepository,
} from "../domain/context.js";
import {
  safeExternalURL,
  repositoryURL,
  repositoryProviderID,
} from "../domain/context.js";
import {
  capabilities,
  providerLabel,
  providerNumber,
} from "../domain/settings.js";
import type { Receipt } from "../domain/settings.js";
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

export function ProjectCatalog({
  client,
  me,
  projects,
  onSaved,
  next,
  loading,
  loadMore,
}: {
  client: ApiClient;
  me: Me;
  projects: Project[];
  onSaved: (id?: string) => Promise<void>;
  next: boolean;
  loading: boolean;
  loadMore: () => void;
}) {
  const [name, setName] = useState("");
  const [id, setID] = useState(() => crypto.randomUUID());
  const action = useMutation(client);
  async function create(event: FormEvent) {
    event.preventDefault();
    const saved = await action.run<Receipt>(
      "POST",
      "/v1/projects",
      { name: name.trim(), id },
      true,
    );
    if (saved) {
      setName("");
      setID(crypto.randomUUID());
      action.setMessage("Проект создан.");
      await onSaved(saved.value.entity_id);
    }
  }
  return (
    <section className="settings-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1>{translate("Каталог проектов")}</h1>
        </div>
      </div>
      {capabilities(me).createProject && (
        <MutationForm
          pending={action.pending}
          className={sharedStyles.setup}
          onSubmit={(event) => void create(event)}
        >
          <h2>{translate("Новый проект")}</h2>
          <label>
            {translate("Название проекта")}
            <input
              required
              value={name}
              maxLength={200}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <button
            className={buttonStyles.primary}
            disabled={action.pending || !name.trim()}
          >
            {action.pending
              ? translate("Создаём…")
              : translate("Создать проект")}
          </button>
          <MutationNotice action={action} />
        </MutationForm>
      )}
      {projects.length === 0 && (
        <Empty title={translate("Нет доступных проектов")}>
          {translate(
            "Оператор установки может создать проект и назначить участников.",
          )}
        </Empty>
      )}
      <div className="settings-list">
        {projects.map((project) => (
          <article
            className={`
              settings-row flex justify-between items-center gap-5 px-0 py-5 border-b border-line
              [&_h2]:mb-2 [&_p]:mb-2 [&_>_div]:min-w-0 mobile:flex-wrap
            `}
            key={project.id}
          >
            <div>
              <h2>{project.name}</h2>
              <p className="mono muted text-muted font-mono text-xs wrap-anywhere">
                {project.id}
              </p>
              <span className="small text-sm">
                {project.role
                  ? translate("Роль: {0}", project.role)
                  : translate("Только доступ к каталогу")}
              </span>
            </div>
            <button
              className={buttonStyles.secondary}
              onClick={() => void onSaved(project.id)}
            >
              {translate("Открыть проект")}
            </button>
          </article>
        ))}
      </div>
      {next && (
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={loadMore}
        >
          {translate("Загрузить ещё проекты")}
        </button>
      )}
    </section>
  );
}

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
  const [open, setOpen] = useState(false);
  return (
    <section className="repository-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1>{translate("Репозитории")}</h1>
        </div>
        {capabilities(me, project).catalog && (
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
      {capabilities(me, project).catalog && open && (
        <RepositoryCreate client={client} project={project} onSaved={onSaved} />
      )}
      {repositories.length === 0 && (
        <Empty title={translate("Репозитории ещё не зарегистрированы")}>
          {translate(
            "Зарегистрируйте репозиторий, затем настройте соединение в настройках проекта.",
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
  const hostID = useId();
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [provider, setProvider] = useState("");
  const [host, setHost] = useState<"gitlab" | "github">("gitlab");
  const [resolved, setResolved] = useState<ResolvedRepository | null>(null);
  const lookup = useMutation(client);
  const [id, setID] = useState(() => crypto.randomUUID());
  const [error, setError] = useState("");
  const action = useMutation(client);
  async function create(event: FormEvent) {
    event.preventDefault();
    setError("");
    try {
      let identity: Record<string, unknown>;
      let repositoryName = name.trim();
      if (host === "github") {
        let value = resolved;
        if (!value) {
          const result = await lookup.run<ResolvedRepository>(
            "POST",
            `/v1/projects/${project.id}/repositories/lookup`,
            { url: url.trim() },
          );
          if (!result) return;
          value = result.value;
          if (
            value.provider !== "github" ||
            !Number.isSafeInteger(value.provider_repository_id) ||
            value.provider_repository_id < 1
          )
            throw new Error("ID вне точного диапазона браузера");
          setResolved(value);
        }
        identity = {
          provider: "github",
          provider_url: value.provider_url,
          provider_repository_id: value.provider_repository_id,
        };
        if (!repositoryName) repositoryName = value.name;
      } else {
        const number = providerNumber(provider);
        if (!safeExternalURL(url))
          throw new Error(
            "Введите полный HTTP(S)-адрес GitLab без учётных данных.",
          );
        identity = { gitlab_url: url.trim(), gitlab_project_id: number };
      }
      const saved = await action.run(
        "POST",
        `/v1/projects/${project.id}/repositories`,
        {
          id,
          name: repositoryName,
          ...identity,
        },
        true,
      );
      if (saved) {
        setID(crypto.randomUUID());
        setName("");
        setProvider("");
        setResolved(null);
        action.setMessage(
          "Репозиторий зарегистрирован. Настройте соединение в настройках проекта.",
        );
        await onSaved();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : translate("Проверьте данные."));
    }
  }
  return (
    <MutationForm
      pending={action.pending || lookup.pending}
      className={sharedStyles.setup}
      onSubmit={(event) => void create(event)}
    >
      <h2>{translate("Новый репозиторий")}</h2>
      <div className="mb-4">
        <label htmlFor={hostID}>{translate("Хостинг репозитория")}</label>
        <select
          id={hostID}
          value={host}
          onChange={(event) => {
            setHost(event.target.value as "gitlab" | "github");
            setURL("");
            setProvider("");
            setResolved(null);
          }}
        >
          <option value="gitlab">GitLab</option>
          <option value="github">GitHub</option>
        </select>
      </div>
      <div className={sharedStyles.formGrid}>
        <label>
          {translate("Название репозитория")}
          <input
            required={host === "gitlab"}
            maxLength={200}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          {host === "github"
            ? translate("Ссылка на репозиторий GitHub")
            : translate("Адрес сервера GitLab")}
          <input
            type="url"
            required
            maxLength={2000}
            placeholder={
              host === "github"
                ? "https://github.com/owner/repository"
                : "https://gitlab.internal"
            }
            value={url}
            onChange={(e) => {
              setURL(e.target.value);
              setResolved(null);
            }}
          />
        </label>
        {host === "gitlab" && (
          <label>
            {translate("Числовой ID проекта GitLab")}
            <input
              inputMode="numeric"
              required
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
            />
          </label>
        )}
      </div>
      <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
        {host === "github"
          ? translate(
              "Укажите ссылку на публичный репозиторий. Gitmora проверит его и определит ID автоматически.",
            )
          : translate(
              "ID указан в настройках проекта GitLab. Используйте адрес сервера без имени проекта. После создания изменить привязку нельзя.",
            )}
      </p>
      <ErrorNotice>{error}</ErrorNotice>
      <MutationNotice action={lookup} />
      <MutationNotice action={action} />
      <button className={buttonStyles.primary} disabled={action.pending}>
        {translate("Зарегистрировать репозиторий")}
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
          <p className="mono muted text-muted font-mono text-xs wrap-anywhere">
            {repository.id}
          </p>
        </div>
        <dl>
          <dt>
            {repository.provider === "github"
              ? translate("Репозиторий GitHub")
              : translate("Проект GitLab")}
          </dt>
          <dd className="mono font-mono text-xs wrap-anywhere">
            {providerLabel(repositoryProviderID(repository))}
          </dd>
        </dl>
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
