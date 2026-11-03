import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useEffect, useRef, useState } from "react";
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
import { RepositoryAccess } from "./connections/RepositoryAccess.js";
import {
  clearRepositoryToken,
  repositoryAccessBody,
  type RepositoryAccessValue,
} from "../domain/repository-access.js";

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
  const [connected, setConnected] = useState(false);
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
        {canConnect && repositories.length > 0 && !open && (
          <button
            className={buttonStyles.primary}
            onClick={() => {
              setOpen(true);
              setConnected(false);
            }}
          >
            {translate("Добавить репозиторий")}
          </button>
        )}
      </div>
      {canConnect && open && (
        <RepositoryCreate
          client={client}
          project={project}
          repositories={repositories}
          {...(repositories.length ? { onCancel: () => setOpen(false) } : {})}
          onRegistered={onSaved}
          onSaved={async (id, first) => {
            await onSaved();
            setOpen(false);
            setConnected(true);
            if (first) onConnected(id);
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
      {connected && (
        <p role="status" className="text-sm text-success my-4">
          {translate("Репозиторий подключён.")}
        </p>
      )}
      {open && repositories.length > 0 && (
        <h2 className="mt-8 mb-4 text-base">
          {translate("Подключённые репозитории")}
        </h2>
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
  repositories,
  onCancel,
}: {
  client: ApiClient;
  project: Project;
  onSaved: (id: string, first: boolean) => Promise<void>;
  onRegistered: () => Promise<void>;
  repositories: Repository[];
  onCancel?: () => void;
}) {
  const busy = useRef(false);
  const [first] = useState(repositories.length === 0);
  const [saving, setSaving] = useState(false);
  const [accessRevision, setAccessRevision] = useState(0);
  const [host, setHost] = useState<"github" | "gitlab">("github");
  const [url, setURL] = useState("");
  const [access, setAccess] = useState<RepositoryAccessValue>({
    kind: "new",
    token: "",
  });
  const [name, setName] = useState("");
  const [id] = useState(() => crypto.randomUUID());
  const [created, setCreated] = useState(false);
  const [resolved, setResolved] = useState<ResolvedRepository | null>(null);
  const action = useMutation(client);
  const lookup = useMutation(client);
  const connection = useMutation(client);
  const [error, setError] = useState("");
  useEffect(() => {
    setAccess({ kind: "new", token: "" });
  }, [host]);
  async function create(event: FormEvent) {
    event.preventDefault();
    if (busy.current) return;
    busy.current = true;
    setSaving(true);
    setError("");
    const credentials = repositoryAccessBody(access);
    setAccess(clearRepositoryToken(access));
    let identity = resolved;
    try {
      if (!created) {
        const found = await lookup.run<ResolvedRepository>(
          "POST",
          `/v1/projects/${project.id}/repositories/lookup`,
          {
            url: url.trim(),
            provider: host,
            ...(access.kind === "public" ? {} : credentials),
          },
        );
        if (!found) {
          if (access.kind === "saved") setAccessRevision((value) => value + 1);
          return;
        }
        identity = found.value;
        if (
          identity.provider !== (host === "github" ? "github" : "") ||
          !Number.isSafeInteger(identity.provider_repository_id) ||
          identity.provider_repository_id < 1
        )
          throw new Error(translate("Проверьте данные репозитория."));
        if (
          repositories.some(
            (repository) =>
              (repository.provider ?? "") === identity!.provider &&
              repositoryURL(repository) === identity!.provider_url &&
              (repository.provider === "github"
                ? repository.provider_repository_id
                : repository.gitlab_project_id) ===
                identity!.provider_repository_id,
          )
        ) {
          setError(translate("Этот репозиторий уже добавлен в проект."));
          return;
        }
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
          ...credentials,
          webhook_mode: "disabled",
        },
      );
      if (saved) await onSaved(id, first);
      else if (access.kind === "saved") setAccessRevision((value) => value + 1);
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
              setAccess({ kind: "new", token: "" });
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
      <RepositoryAccess
        key={host}
        client={client}
        projectID={project.id}
        provider={host}
        url={url}
        value={access}
        onChange={setAccess}
        refreshKey={accessRevision}
      />
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
            "Репозиторий добавлен. Выберите доступ, чтобы завершить подключение.",
          )}
        </p>
      )}
      <div className="flex flex-wrap gap-3 border-t border-line pt-4">
        <button className={buttonStyles.primary}>
          {saving
            ? translate("Подключаем…")
            : created
              ? translate("Сохранить подключение")
              : translate("Подключить репозиторий")}
        </button>
        {onCancel && (
          <button
            className={buttonStyles.secondary}
            type="button"
            onClick={onCancel}
          >
            {translate("Отмена")}
          </button>
        )}
      </div>
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
      <div className="repository-row flex flex-wrap items-center justify-between gap-4 p-4 mobile:flex-col mobile:items-stretch">
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
        <div className="flex flex-wrap gap-2 mobile:grid mobile:grid-cols-2">
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
