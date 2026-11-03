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
import { Members, ProjectSettings } from "./ProjectSettings.js";
import { SetupGuide } from "./AgentSetup.js";
import { capabilities } from "../domain/settings.js";

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
  view: "context" | "repositories" | "settings" | "members" | "setup";
  refreshProject: (deleted?: boolean) => Promise<void>;
  refreshing: boolean;
}) {
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  const [setupRepository, setSetupRepository] = useState("");
  useEffect(() => {
    const read = () => {
      const p = new URLSearchParams(location.hash.slice(1));
      if (p.get("project") === project.id && p.get("view") === "setup")
        setSetupRepository(p.get("repository") ?? "");
    };
    read();
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, [project.id]);
  const chosenRepository =
    repositories.find((r) => r.id === setupRepository) ?? repositories[0];
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
      if (request.current()) {
        setLoading(false);
        setLoaded(true);
      }
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
      {view === "members" ? (
        <Members
          client={client}
          project={project}
          canEdit={capabilities(me, project).catalog}
          refreshProject={() => refreshProject()}
        />
      ) : view === "setup" ? (
        <section>
          <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
            <h1 className="mb-0">{translate("CLI и MCP")}</h1>
            <a
              href="/ai.md"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm font-medium"
            >
              {translate("Инструкция для агента · ai.md")}
            </a>
          </div>
          {repositories.length > 1 && (
            <label className="block max-w-xl mb-6">
              {translate("Репозиторий")}
              <select
                className="w-full"
                value={chosenRepository?.id ?? ""}
                onChange={(event) => setSetupRepository(event.target.value)}
              >
                {repositories.map((repository) => (
                  <option key={repository.id} value={repository.id}>
                    {repository.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <SetupGuide
            key={chosenRepository?.id ?? "none"}
            {...(chosenRepository ? { repositoryID: chosenRepository.id } : {})}
          />
        </section>
      ) : !loaded && loading ? (
        <p role="status">{translate("Загружаем репозитории…")}</p>
      ) : view === "context" &&
        (!project.role ||
          !me.scopes.some((scope) =>
            ["capture:read", "integration:read"].includes(scope),
          )) ? (
        <Empty heading="h1" title={translate("Нет доступа к контексту")}>
          {translate("Выберите проект, к которому у вас есть доступ.")}
        </Empty>
      ) : view === "repositories" ||
        (repositories.length === 0 && !error && view !== "settings") ? (
        <RepositoryCatalog
          client={client}
          me={me}
          project={project}
          repositories={repositories}
          onSaved={async () => {
            await load();
            await refreshProject();
          }}
          onConnected={(id) => {
            location.hash = new URLSearchParams({
              view: "setup",
              project: project.id,
              repository: id,
            }).toString();
          }}
        />
      ) : view === "settings" ? (
        <ProjectSettings
          client={client}
          me={me}
          project={project}
          refreshProject={refreshProject}
        />
      ) : (
        <ContextWorkspace
          client={client}
          project={project}
          repositories={repositories}
          me={me}
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
