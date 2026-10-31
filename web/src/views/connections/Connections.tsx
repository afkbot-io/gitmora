import { t as translate } from "../../domain/locale.js";
import { useEffect, useState } from "react";
import type { Project, Repository } from "../../domain/context.js";
import type { ApiClient } from "../../transport/api.js";
import { GitLabConnection } from "./GitLabConnection.js";
import { TasktopiaConnection } from "./TasktopiaConnection.js";

export function Connections({
  client,
  project,
  repositories,
  canRead,
  canManage,
}: {
  client: ApiClient;
  project: Project;
  repositories: Repository[];
  canRead: boolean;
  canManage: boolean;
}) {
  const [selected, setSelected] = useState("");
  useEffect(() => {
    if (!repositories.some((item) => item.id === selected))
      setSelected(repositories[0]?.id ?? "");
  }, [repositories, selected]);
  const repository = repositories.find((item) => item.id === selected);
  return (
    <>
      <TasktopiaConnection
        client={client}
        project={project}
        canRead={canRead}
        canManage={canManage}
      />
      <section className="settings-section border-t border-line px-0 py-7 mt-6 [&_h2]:text-2xl [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0">
        <h2>GitLab</h2>
        <p className="muted text-muted">
          {translate("Синхронизация коммитов, merge requests и CI.")}
        </p>
        {repositories.length ? (
          <label className="repository-choice max-w-110 flex flex-col gap-2">
            {translate("Репозиторий интеграции")}
            <select
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
            >
              {repositories.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <p>
            {translate(
              "Сначала зарегистрируйте репозиторий в разделе «Репозитории».",
            )}
          </p>
        )}
        {repository && (
          <GitLabConnection
            key={repository.id}
            client={client}
            repository={repository}
            canRead={canRead}
            canManage={canManage}
          />
        )}
      </section>
    </>
  );
}
