import { t as translate } from "../domain/locale.js";
import { workspaceView, type WorkspaceView } from "../domain/navigation.js";
import { useEffect, useRef, useState } from "react";
import { LatestRequest } from "../application/latest.js";
import { readableError } from "../domain/context.js";
import { capabilities } from "../domain/settings.js";
import type { AuthConfig, Me, Page, Project } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { Brand, ErrorNotice, aborted } from "./shared.js";
import { ProjectWorkspace } from "./ProjectWorkspace.js";
import { Preferences } from "./Preferences.js";
import { Keys } from "./credentials/Keys.js";
import { CreateProject } from "./CreateProject.js";
import { Icon } from "./Icon.js";
import { buttonStyles } from "./ui.js";

export function Workspace({
  client,
  me,
  logout,
  onMe,
  notice,
  clearNotice,
}: {
  client: ApiClient;
  me: Me;
  logout: () => Promise<void>;
  onMe: (v: Me) => void;
  authConfig: AuthConfig;
  notice: string;
  clearNotice: () => void;
}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState("");
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [view, setCurrentView] = useState<WorkspaceView>(() =>
    workspaceView(location.hash),
  );
  const [menuOpen, setMenuOpen] = useState(false);
  const [requests] = useState(() => new LatestRequest());
  const main = useRef<HTMLElement>(null);
  const project = projects.find((p) => p.id === selected);
  const canCreate = capabilities(me).createProject;
  const storageKey = "gitmora.project." + me.user.id;
  function remember(id: string) {
    setSelected(id);
    try {
      localStorage.setItem(storageKey, id);
    } catch {
      /* Selection remains usable without storage. */
    }
  }
  function navigate(target: WorkspaceView, id = selected) {
    const params = new URLSearchParams();
    params.set("view", target);
    if (id) params.set("project", id);
    location.hash = params.toString();
    setCurrentView(target);
    setMenuOpen(false);
  }
  useEffect(() => {
    const read = () => {
      setCurrentView(workspaceView(location.hash));
      setMenuOpen(false);
      const id = new URLSearchParams(location.hash.slice(1)).get("project");
      if (id && projects.some((p) => p.id === id)) remember(id);
    };
    window.addEventListener("hashchange", read);
    read();
    return () => window.removeEventListener("hashchange", read);
  }, [projects]);
  async function load(after = "") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<Project>>(
        "/v1/projects",
        undefined,
        request.signal,
        { limit: 100, ...(after ? { after_id: after } : {}) },
      );
      if (!request.current()) return;
      setProjects((current) =>
        after ? [...current, ...page.items] : page.items,
      );
      setNext(page.next_id ?? "");
      if (!after) {
        let saved = "";
        try {
          saved = localStorage.getItem(storageKey) ?? "";
        } catch {
          /* Optional preference. */
        }
        const fromURL = new URLSearchParams(location.hash.slice(1)).get(
          "project",
        );
        remember(
          page.items.find((p) => p.id === (fromURL || selected || saved))?.id ??
            (page.items.find((p) => p.role) ?? page.items[0])?.id ??
            "",
        );
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
  }, [client, requests]);
  async function refreshProject(deleted = false) {
    if (deleted) {
      remember("");
      navigate("context", "");
      await load();
      return;
    }
    if (!project) return;
    const request = requests.begin();
    setLoading(true);
    try {
      const value = await client.request<Project>(
        `/v1/projects/${project.id}`,
        undefined,
        request.signal,
      );
      if (request.current())
        setProjects((items) =>
          items.map((p) => (p.id === value.id ? value : p)),
        );
    } catch (e) {
      if (!aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  async function created(id: string) {
    const value = await client.request<Project>(`/v1/projects/${id}`);
    setProjects((items) => [...items.filter((p) => p.id !== id), value]);
    remember(id);
    setCreating(false);
    navigate("repositories", id);
  }
  const navigation = [
    { view: "context", label: "Поиск", icon: "context" },
    { view: "repositories", label: "Репозитории", icon: "repositories" },
    { view: "members", label: "Участники", icon: "members" },
    { view: "settings", label: "Настройки проекта", icon: "settings" },
    { view: "setup", label: "CLI и MCP", icon: "setup" },
  ] as const;
  return (
    <div className="workspace grid min-h-dvh w-full grid-cols-[232px_minmax(0,1fr)] mobile:block">
      <a
        className="skip absolute -top-25 left-64 z-50 bg-paper p-3 focus:top-3 mobile:left-4"
        href="#workspace-main"
      >
        {translate("К содержимому")}
      </a>
      <aside className="sidebar sticky top-0 flex h-dvh min-w-0 flex-col overflow-y-auto border-r border-line bg-paper mobile:static mobile:h-auto mobile:overflow-visible mobile:border-r-0 mobile:border-b">
        <div className="flex items-center justify-between gap-3 px-4 py-4 mobile:py-3">
          <Brand />
          <button
            className="hidden mobile:inline-flex p-2 min-h-10 bg-canvas"
            aria-label={translate("Разделы проекта")}
            aria-expanded={menuOpen}
            aria-controls="project-navigation"
            onClick={() => setMenuOpen(!menuOpen)}
          >
            ☰
          </button>
        </div>
        <div className="px-3 pb-4 mobile:pb-3">
          <div className="mb-2 flex items-center justify-between gap-2">
            <label
              htmlFor="project"
              className="text-xs text-muted mobile:sr-only"
            >
              {translate("Проект")}
            </label>
            {canCreate && project && (
              <button
                className="min-h-8 min-w-8 rounded-md border-0 bg-transparent p-1 text-lg text-muted hover:bg-canvas hover:text-ink"
                aria-label={translate("Создать проект")}
                title={translate("Создать проект")}
                onClick={() => setCreating(true)}
              >
                ＋
              </button>
            )}
          </div>
          <select
            id="project"
            className="w-full text-sm font-medium"
            value={selected}
            disabled={loading || !projects.length}
            onChange={(e) => {
              requests.cancel();
              setLoading(false);
              remember(e.target.value);
              navigate(view, e.target.value);
            }}
          >
            {!project && (
              <option value="">{translate("Выберите проект")}</option>
            )}
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          {next && (
            <button
              className={buttonStyles.text}
              onClick={() => void load(next)}
            >
              {translate("Загрузить ещё")}
            </button>
          )}
        </div>
        <nav
          id="project-navigation"
          aria-label={translate("Разделы проекта")}
          className={`flex flex-col gap-1 px-3 mobile:pb-3 ${menuOpen ? "" : "mobile:hidden"}`}
        >
          {project &&
            navigation.map((item) => (
              <a
                key={item.view}
                href={`#view=${item.view}&project=${selected}`}
                aria-current={view === item.view ? "page" : undefined}
                className={`flex min-h-11 items-center gap-3 rounded-md px-3 py-2 text-sm font-medium no-underline ${view === item.view ? "bg-accent-soft text-accent" : "text-muted hover:bg-canvas hover:text-ink"}`}
                onClick={(event) => {
                  if (
                    event.metaKey ||
                    event.ctrlKey ||
                    event.shiftKey ||
                    event.altKey ||
                    event.button !== 0
                  )
                    return;
                  if (menuOpen)
                    requestAnimationFrame(() => main.current?.focus());
                  setCurrentView(item.view);
                  setMenuOpen(false);
                }}
              >
                <Icon name={item.icon} />
                {translate(item.label)}
              </a>
            ))}
        </nav>
        <div className="account mt-auto border-t border-line p-4 mobile:flex mobile:items-center mobile:justify-between mobile:gap-3">
          <a
            href={`#view=preferences${selected ? "&project=" + selected : ""}`}

            className="flex min-w-0 items-center gap-3 text-ink no-underline hover:text-accent"
            onClick={() => setCurrentView("preferences")}
          >
            <span
              aria-hidden="true"
              className="flex size-9 shrink-0 items-center justify-center rounded-full bg-accent-soft text-accent font-semibold"
            >
              {Array.from(me.user.display_name)[0]?.toUpperCase() ?? "G"}
            </span>
            <span className="min-w-0">
              <strong className="block truncate text-sm">
                {me.user.username
                  ? "@" + me.user.username
                  : me.user.display_name}
              </strong>
              <span className="block text-xs text-muted">
                {translate("Личные настройки")}
              </span>
            </span>
          </a>
          <button
            className={buttonStyles.text + " mt-2 mobile:mt-0"}
            onClick={() => void logout()}
          >
            {translate("Выйти")}
          </button>
        </div>
      </aside>
      <main
        id="workspace-main"
        ref={main}
        tabIndex={-1}
        className="main min-w-0 min-h-dvh"
      >
        <div className="p-6 mobile:p-4">
          <ErrorNotice>{error}</ErrorNotice>
          {notice && (
            <div>
              <ErrorNotice>{notice}</ErrorNotice>
              <button className={buttonStyles.text} onClick={clearNotice}>
                {translate("Скрыть уведомление")}
              </button>
            </div>
          )}
          {error && (
            <button
              className={buttonStyles.secondary}
              onClick={() => void load()}
            >
              {translate("Повторить загрузку")}
            </button>
          )}
          {view === "preferences" || view === "keys" ? (
            <>
              <Preferences client={client} me={me} onMe={onMe} />
              {me.scopes.includes("credentials:manage") && (
                <details className="mt-8 border-t border-line pt-5">
                  <summary className="text-sm font-medium">
                    {translate("Ключи для автоматизации")}
                  </summary>
                  <div className="mt-5">
                    <Keys client={client} me={me} />
                  </div>
                </details>
              )}
            </>
          ) : loading && !projects.length ? (
            <p role="status">{translate("Загружаем проекты…")}</p>
          ) : !project ? (
            <section className="mx-auto grid min-h-[calc(100dvh-3rem)] w-full max-w-5xl grid-cols-2 items-center gap-12 compact:grid-cols-1 compact:content-center compact:gap-6 mobile:min-h-0 mobile:py-6">
              <div>
                <h1 className="mb-3 text-3xl mobile:text-2xl">
                  {translate("Создайте первый проект")}
                </h1>
                <p className="text-muted">
                  {translate(
                    "Объедините репозитории и команду в одном проекте.",
                  )}
                </p>
                <ol className="mt-7 grid list-none gap-4 p-0 text-sm">
                  {[
                    "Создайте проект",
                    "Подключите GitHub или GitLab",
                    "Включите запись сессий в CLI",
                  ].map((step, index) => (
                    <li className="flex items-center gap-3" key={step}>
                      <span className="flex size-7 shrink-0 items-center justify-center rounded-full border border-line bg-paper text-xs text-muted">
                        {index + 1}
                      </span>
                      {translate(step)}
                    </li>
                  ))}
                </ol>
              </div>
              {canCreate && (
                <div className="rounded-xl border border-line bg-paper p-6 mobile:p-4">
                  <CreateProject client={client} onCreated={created} />
                </div>
              )}
            </section>
          ) : (
            <ProjectWorkspace
              key={project.id}
              client={client}
              project={project}
              me={me}
              view={view}
              refreshProject={refreshProject}
              refreshing={loading}
            />
          )}
        </div>
      </main>
      {creating && (
        <CreateProject
          client={client}
          onCreated={created}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  );
}
