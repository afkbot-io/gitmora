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
    <section className="settings-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <h1>{translate("Настройки проекта")}</h1>
        <button
          className={buttonStyles.secondary}
          onClick={() => void refreshProject()}
        >
          {translate("Обновить проект")}
        </button>
      </div>
      <section className="settings-section border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0">
        <h2>{translate("Проект")}</h2>
        <p className="mono muted text-muted font-mono text-xs wrap-anywhere">
          {project.id}
        </p>
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
          <p className="wrap-anywhere">{project.name}</p>
        )}
      </section>
      {rights.members && (
        <Members
          client={client}
          project={project}
          canEdit={rights.catalog}
          refreshProject={() => refreshProject()}
        />
      )}
      {rights.integrationRead || rights.integrations ? (
        <Connections
          client={client}
          project={project}
          repositories={repositories}
          canRead={rights.integrationRead}
          canManage={rights.integrations}
        />
      ) : (
        <section className="settings-section border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0">
          <h2>{translate("Интеграции")}</h2>
          <p className="muted text-muted">
            {translate("Интеграции настраивает администратор проекта.")}
          </p>
        </section>
      )}
      {rights.catalog && (
        <section
          className={`
          settings-section danger-zone border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl
          [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0 max-w-220 [&_p]:text-muted [&_p]:max-w-[65ch]
          [&_p]:text-sm
        `}
        >
          <h2>{translate("Удаление проекта")}</h2>
          <p>
            {translate(
              "Удаляет связанные данные Gitmora. Репозитории и задачи во внешних сервисах сохраняются.",
            )}
          </p>
          <button
            className={buttonStyles.dangerOutline}
            onClick={() => setDeleting(true)}
          >
            {translate("Удалить проект")}
          </button>
        </section>
      )}
      {rights.catalog && deleting && (
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
              "Будут удалены локальные репозитории, сессии, индексы, соединения и результаты проекта. Для повторной регистрации потребуется новый идентификатор.",
            )}
          </p>
        </ConfirmAction>
      )}
    </section>
  );
}
function Members({
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
    if (
      await action.run(
        "PUT",
        `/v1/projects/${project.id}/members/${user.trim()}`,
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
    <section className="settings-section border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0">
      <div className={sharedStyles.heading}>
        <h2>{translate("Участники")}</h2>
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
          {translate("Добавьте существующую учётную запись по её UUID.")}
        </Empty>
      )}
      {canEdit && (
        <MutationForm
          pending={action.pending}
          onSubmit={(event) => void add(event)}
          className={sharedStyles.setup}
        >
          <h3>{translate("Добавить участника")}</h3>
          <div className={sharedStyles.formGrid}>
            <label>
              {translate("UUID учётной записи")}
              <input
                required
                pattern="[0-9a-fA-F-]{36}"
                value={user}
                onChange={(e) => setUser(e.target.value)}
                autoComplete="off"
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              />
            </label>
            <label>
              {translate("Роль нового участника")}
              <select value={role} onChange={(e) => setRole(e.target.value)}>
                <RoleOptions />
              </select>
            </label>
          </div>
          <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
            {translate("Добавьте существующего пользователя Gitmora.")}
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
          {member.user_id}
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
            <select value={role} onChange={(e) => setRole(e.target.value)}>
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
