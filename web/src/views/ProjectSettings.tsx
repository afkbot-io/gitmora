import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Me, Page, Project, Repository } from "../domain/context.js";
import { readableError } from "../domain/context.js";
import { capabilities } from "../domain/settings.js";
import type { Member } from "../domain/settings.js";
import { LatestRequest } from "../application/latest.js";
import { useMutation } from "../application/useMutation.js";
import type { ApiClient } from "../transport/api.js";
import {
  ConfirmAction,
  MutationNotice,
  Rename,
  MutationForm,
} from "./SettingsShared.js";
import { Empty, ErrorNotice, aborted } from "./shared.js";
import { Connections } from "./connections/Connections.js";
export function ProjectSettings({
  client,
  me,
  project,
  repositories,
  refreshProject,
}: {
  client: ApiClient;
  me: Me;
  project: Project;
  repositories: Repository[];
  refreshProject: (deleted?: boolean) => Promise<void>;
}) {
  const rights = capabilities(me, project);
  const [deleting, setDeleting] = useState(false);
  const action = useMutation(client);
  const [tab, setTab] = useState(() =>
    new URLSearchParams(location.hash.slice(1)).get("tab") === "integrations"
      ? "integrations"
      : "general",
  );
  useEffect(() => {
    const read = () =>
      setTab(
        new URLSearchParams(location.hash.slice(1)).get("tab") ===
          "integrations"
          ? "integrations"
          : "general",
      );
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, []);
  async function remove() {
    if (
      await action.run(
        "DELETE",
        `/v1/projects/${project.id}`,
        { expected_version: project.version },
        true,
      )
    ) {
      setDeleting(false);
      await refreshProject(true);
    }
  }
  return (
    <section className="settings-view">
      <h1>{translate("Настройки проекта")}</h1>
      <nav
        aria-label={translate("Настройки проекта")}
        className="flex gap-6 border-b border-line mb-6"
      >
        {[
          ["general", "Основное"],
          ["integrations", "Интеграции"],
        ].map(([key, label]) => (
          <a
            key={key}
            href={`#view=settings&project=${project.id}&tab=${key}`}
            aria-current={tab === key ? "page" : undefined}
            className={`py-3 text-sm no-underline border-b-2 ${tab === key ? "border-accent font-medium text-accent" : "border-transparent text-muted"}`}
            onClick={() => setTab(key!)}
          >
            {translate(label!)}
          </a>
        ))}
      </nav>
      {tab === "general" ? (
        <>
          <section className="settings-section grid grid-cols-[220px_minmax(0,1fr)] gap-6 border-b border-line pb-8 mobile:grid-cols-1">
            <h2 className="text-base">{translate("Проект")}</h2>
            <div>
              {rights.catalog ? (
                <Rename
                  key={project.version}
                  client={client}
                  path={`/v1/projects/${project.id}`}
                  name={project.name}
                  version={project.version}
                  onSaved={() => refreshProject()}
                  label={translate("Название проекта")}
                />
              ) : (
                <p>{project.name}</p>
              )}
              <details className="mt-5 text-xs text-muted">
                <summary>{translate("Идентификатор проекта")}</summary>
                <code className="block mt-3 break-all">{project.id}</code>
              </details>
            </div>
          </section>
          {rights.catalog && (
            <section className="settings-section grid grid-cols-[220px_minmax(0,1fr)] gap-6 py-8 mobile:grid-cols-1">
              <h2 className="text-base">{translate("Удаление проекта")}</h2>
              <div>
                <p className="text-sm text-muted">
                  {translate("Удаляет историю и настройки проекта в Gitmora.")}
                </p>
                <button
                  className={buttonStyles.dangerOutline}
                  onClick={() => setDeleting(true)}
                >
                  {translate("Удалить проект")}
                </button>
              </div>
            </section>
          )}
        </>
      ) : (
        <Connections
          client={client}
          project={project}
          repositories={repositories}
          canRead={rights.integrationRead}
          canManage={rights.integrations}
        />
      )}
      {deleting && (
        <ConfirmAction
          title={translate("Удалить проект {0}?", project.name)}
          pending={action.pending}
          error={action.error}
          onClose={() => setDeleting(false)}
          onConfirm={() => void remove()}
          label={translate("Удалить проект и его данные")}
        >
          <p>
            {translate(
              "История Gitmora будет удалена. Исходный код и задачи во внешних сервисах сохранятся.",
            )}
          </p>
        </ConfirmAction>
      )}
    </section>
  );
}
export function Members({
  client,
  project,
  canEdit,
  refreshProject,
}: {
  client: ApiClient;
  project: Project;
  canEdit: boolean;
  refreshProject: () => Promise<void>;
}) {
  const [items, setItems] = useState<Member[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  const [user, setUser] = useState("");
  const [role, setRole] = useState("reader");
  const action = useMutation(client);
  async function load(after = "") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<Member>>(
        `/v1/projects/${project.id}/members`,
        undefined,
        request.signal,
        { limit: 100, ...(after ? { after_id: after } : {}) },
      );
      if (request.current()) {
        setItems((current) =>
          after ? [...current, ...page.items] : page.items,
        );
        setNext(page.next_id ?? "");
      }
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, project.id, requests]);
  async function add(event: FormEvent) {
    event.preventDefault();
    let candidate: { id: string };
    try {
      candidate = await client.request<{ id: string }>(
        `/v1/projects/${project.id}/members/resolve`,
        { identifier: user.trim() },
      );
    } catch (e) {
      setError(readableError(e));
      return;
    }
    if (items.some((m) => m.user_id === candidate.id)) {
      setError(translate("Этот пользователь уже в проекте."));
      return;
    }
    setError("");
    if (
      await action.run(
        "PUT",
        `/v1/projects/${project.id}/members/${candidate.id}`,
        { expected_version: 0, role },
        true,
      )
    ) {
      setUser("");
      action.setMessage("Участник добавлен.");
      await load();
      await refreshProject();
    }
  }
  return (
    <section className="members-view">
      <div className={sharedStyles.heading}>
        <h1>{translate("Участники")}</h1>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {translate("Обновить участников")}
        </button>
      </div>
      <ErrorNotice>{error}</ErrorNotice>
      <div className="settings-list">
        {items.map((member) => (
          <MemberRow
            key={`${member.user_id}-${member.version}`}
            client={client}
            project={project}
            member={member}
            canEdit={canEdit}
            onSaved={async () => {
              await load();
              await refreshProject();
            }}
          />
        ))}
      </div>
      {next && (
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load(next)}
        >
          {translate("Загрузить ещё участников")}
        </button>
      )}
      {!items.length && !loading && !error && (
        <Empty title={translate("Участников нет")}>
          {translate("Добавьте коллегу по нику или UUID.")}
        </Empty>
      )}
      {canEdit && (
        <MutationForm
          pending={action.pending}
          onSubmit={(event) => void add(event)}
          className={sharedStyles.setup}
        >
          <h2 className="text-lg">{translate("Добавить участника")}</h2>
          <div className={sharedStyles.formGrid}>
            <label>
              {translate("Ник или UUID пользователя")}
              <input
                required
                maxLength={80}
                spellCheck={false}
                value={user}
                onChange={(e) => setUser(e.target.value)}
                autoComplete="off"
                placeholder={translate("Например, alex…")}
              />
            </label>
            <label>
              {translate("Роль нового участника")}
              <select
                aria-label={translate("Роль нового участника")}
                value={role}
                onChange={(e) => setRole(e.target.value)}
              >
                <RoleOptions />
              </select>
            </label>
          </div>
          <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
            {translate("Участнику нужна учётная запись Gitmora.")}
          </p>
          <MutationNotice action={action} />
          <button className={buttonStyles.primary} disabled={action.pending}>
            {translate("Добавить участника")}
          </button>
        </MutationForm>
      )}
    </section>
  );
}
function RoleOptions() {
  return (
    <>
      <option value="reader">{translate("Чтение")}</option>
      <option value="writer">{translate("Участник")}</option>
      <option value="admin">{translate("Администратор")}</option>
    </>
  );
}
function MemberRow({
  client,
  project,
  member,
  canEdit,
  onSaved,
}: {
  client: ApiClient;
  project: Project;
  member: Member;
  canEdit: boolean;
  onSaved: () => Promise<void>;
}) {
  const [role, setRole] = useState(member.role);
  const [removing, setRemoving] = useState(false);
  const action = useMutation(client);
  const path = `/v1/projects/${project.id}/members/${member.user_id}`;
  async function change(event: FormEvent) {
    event.preventDefault();
    if (
      await action.run(
        "PUT",
        path,
        { expected_version: member.version, role },
        true,
      )
    )
      await onSaved();
  }
  async function remove() {
    if (
      await action.run(
        "DELETE",
        path,
        { expected_version: member.version },
        true,
      )
    ) {
      setRemoving(false);
      await onSaved();
    }
  }
  return (
    <article
      className={`
      settings-row member-row wrap-anywhere flex justify-between gap-5 px-0 py-5 border-b border-line
      [&_h2]:mb-2 [&_p]:mb-2 [&_>_div]:min-w-0 items-start flex-wrap
      mobile:flex-wrap
    `}
    >
      <div>
        <strong>{member.display_name}</strong>
        {!member.enabled && (
          <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
            {translate("Учётная запись выключена")}
          </span>
        )}
        <p className="mono muted text-muted font-mono text-xs wrap-anywhere">
          {member.username ? "@" + member.username : member.user_id}
        </p>
      </div>
      {canEdit ? (
        <MutationForm
          pending={action.pending}
          onSubmit={(event) => void change(event)}
          className={`
            member-controls flex flex-wrap items-end gap-3 [&_label]:text-xs [&_label]:mb-0 [&_label]:flex
            [&_label]:flex-col [&_label]:gap-1 [&_.notice]:w-full mobile:w-full
            mobile:[&_label]:w-full mobile:[&_select]:w-full
          `}
        >
          <label>
            {translate("Роль")} {member.display_name}
            <select
              aria-label={translate("Роль нового участника")}
              value={role}
              onChange={(e) => setRole(e.target.value)}
            >
              <RoleOptions />
            </select>
          </label>
          <button
            className={buttonStyles.secondary}
            disabled={action.pending || role === member.role}
          >
            {translate("Сохранить роль")}
          </button>
          <button
            type="button"
            className={sharedStyles.destructiveText}
            onClick={() => setRemoving(true)}
          >
            {translate("Убрать доступ")}
          </button>
          <MutationNotice action={action} />
        </MutationForm>
      ) : (
        <span>{member.role}</span>
      )}
      {canEdit && removing && (
        <ConfirmAction
          title={translate("Убрать доступ {0}?", member.display_name)}
          pending={action.pending}
          error={action.error}
          onClose={() => setRemoving(false)}
          onConfirm={() => void remove()}
          label={translate("Убрать доступ")}
        >
          <p>
            {translate(
              "Будет удалено членство в этом проекте. Учётная запись и доступ к другим проектам сохранятся.",
            )}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
