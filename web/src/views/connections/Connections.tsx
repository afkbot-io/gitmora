import { t as translate } from "../../domain/locale.js";
import { useId, useState } from "react";
import type { Project, Repository } from "../../domain/context.js";
import type { ApiClient } from "../../transport/api.js";
import { ForgeConnection } from "./ForgeConnection.js";
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
  const id = useId();
  const [selected, setSelected] = useState(repositories[0]?.id ?? "");
  const repository =
    repositories.find((r) => r.id === selected) ?? repositories[0];
  return (
    <div className="grid gap-8">
      <section className="grid grid-cols-[220px_minmax(0,1fr)] gap-6 border-b border-line pb-8 mobile:grid-cols-1">
        <div>
          <h2 className="text-base">{translate("Репозитории")}</h2>
          <p className="text-sm text-muted">
            {translate("Коммиты и запросы на слияние.")}
          </p>
        </div>
        <div className="min-w-0">
          {repository ? (
            <>
              {repositories.length > 1 ? (
                <>
                  <label htmlFor={id}>{translate("Репозиторий")}</label>
                  <select
                    id={id}
                    className="w-full"
                    value={repository.id}
                    onChange={(e) => setSelected(e.target.value)}
                  >
                    {repositories.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name}
                      </option>
                    ))}
                  </select>
                </>
              ) : (
                <h3 className="text-base font-medium mt-0">
                  {repository.name}
                </h3>
              )}
              <ForgeConnection
                key={repository.id}
                client={client}
                repository={repository}
                canRead={canRead}
                canManage={canManage}
              />
            </>
          ) : (
            <div>
              <p className="text-sm text-muted">
                {translate(
                  "Подключите GitHub или GitLab, чтобы начать работу.",
                )}
              </p>
              <a
                className="action-link bg-action text-white"
                href={`#view=repositories&project=${project.id}`}
              >
                {translate("Подключить репозиторий")}
              </a>
            </div>
          )}
        </div>
      </section>
      <TasktopiaConnection
        client={client}
        project={project}
        canRead={canRead}
        canManage={canManage}
      />
    </div>
  );
}
