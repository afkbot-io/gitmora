import { t as translate } from "../domain/locale.js";
import { buttonStyles } from "./ui.js";
import { useEffect, useState } from "react";
import { LatestRequest } from "../application/latest.js";
import { readableError } from "../domain/context.js";
import type { Me, Page, Project, Repository } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { ErrorNotice, Empty, aborted } from "./shared.js";
import { ContextWorkspace } from "./ContextWorkspace.js";
import { RepositoryCatalog } from "./Catalog.js";
import { ProjectSettings } from "./ProjectSettings.js";
import { WorkWorkspace } from "./work/WorkWorkspace.js";

export function ProjectWorkspace({
  client,
  project,
  me,
  view,
  refreshProject,
  refreshing,
}: {
  client: ApiClient;
  project: Project;
  me: Me;
  view: "context" | "repositories" | "settings" | "work";
  refreshProject: (deleted?: boolean) => Promise<void>;
  refreshing: boolean;
}) {
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(after = "") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<Repository>>(
        `/v1/projects/${project.id}/repositories`,
        undefined,
        request.signal,
        { limit: 100, ...(after ? { after_id: after } : {}) },
      );
      if (!request.current()) return;
      if (page.items.some((item) => item.project_id !== project.id))
        throw new Error("Сервер вернул репозитории другого проекта.");
      setRepositories((current) =>
        after ? [...current, ...page.items] : page.items,
      );
      setNext(page.next_id ?? "");
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
  return (
    <fieldset
      className="project-fields border-0 p-0 m-0 min-w-0 w-full"
      disabled={refreshing}
      aria-label={translate("Действия проекта")}
    >
      <ErrorNotice>{error}</ErrorNotice>
      {error && (
        <button className={buttonStyles.secondary} onClick={() => void load()}>
          {translate("Повторить загрузку репозиториев")}
        </button>
      )}
      {view === "repositories" ? (
        <RepositoryCatalog
          client={client}
          me={me}
          project={project}
          repositories={repositories}
          onSaved={async () => {
            await load();
            await refreshProject();
          }}
        />
      ) : view === "settings" ? (
        <ProjectSettings
          client={client}
          me={me}
          project={project}
          repositories={repositories}
          refreshProject={refreshProject}
        />
      ) : !project.role ||
        !me.scopes.some((scope) =>
          ["capture:read", "integration:read"].includes(scope),
        ) ? (
        <Empty title={translate("Нет доступа к контексту")}>
          {translate(
            "Нужны членство в проекте и разрешение на чтение соответствующих источников.",
          )}
        </Empty>
      ) : view === "work" ? (
        <WorkWorkspace
          client={client}
          project={project}
          repositories={repositories}
          me={me}
        />
      ) : (
        <ContextWorkspace
          client={client}
          project={project}
          repositories={repositories}
        />
      )}
      {next && (
        <button
          className="secondary load-repositories bg-paper text-ink border-control mx-0 my-4"
          disabled={loading}
          onClick={() => void load(next)}
        >
          {translate("Загрузить ещё репозитории")}
        </button>
      )}
    </fieldset>
  );
}
