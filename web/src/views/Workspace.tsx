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
    <div className={`workspace ${project ? "" : "workspace-onboarding"}`}>
      <a
        className="skip absolute -top-25 left-64 z-50 bg-paper p-3 focus:top-3 mobile:left-4"
        href="#workspace-main"
      >
        {translate("К содержимому")}
      </a>
      <header className="workspace-header">
        <div className="workspace-brand">
          {project && (
            <button
              className="navigation-toggle"
              aria-label={translate("Разделы проекта")}
              aria-expanded={menuOpen}
              aria-controls="project-navigation"
              onClick={() => setMenuOpen(!menuOpen)}
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.7"
                aria-hidden="true"
              >
                <path d="M4 6h16M4 12h16M4 18h16" />
              </svg>
            </button>
          )}
          <a
            href={`#view=context${selected ? "&project=" + selected : ""}`}
            className="text-ink no-underline"
          >
            <Brand />
          </a>
        </div>
        {projects.length > 0 && (
          <div className="workspace-project">
            <span className="project-avatar" aria-hidden="true">
              {Array.from(project?.name ?? "G")[0]?.toUpperCase()}
            </span>
            <label htmlFor="project" className="sr-only">
              {translate("Проект")}
            </label>
            <select
              id="project"
              className="project-select"
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
            {canCreate && project && (
              <button
                className="icon-button"
                aria-label={translate("Создать проект")}
                title={translate("Создать проект")}
                onClick={() => setCreating(true)}
              >
                <svg
                  width="18"
                  height="18"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.7"
                  aria-hidden="true"
                >
                  <path d="M12 5v14M5 12h14" />
                </svg>
              </button>
            )}
            {next && (
              <button
                className={buttonStyles.text}
                onClick={() => void load(next)}
              >
                {translate("Загрузить ещё")}
              </button>
            )}
          </div>
        )}
        <div className="account workspace-account">
          <a
            href={`#view=preferences${selected ? "&project=" + selected : ""}`}
            aria-label={`${me.user.username ? "@" + me.user.username : me.user.display_name} · ${translate("Личные настройки")}`}
            title={translate("Личные настройки")}
            className={`account-link ${view === "preferences" || view === "keys" ? "bg-accent-soft" : ""}`}
            onClick={(event) => {
              if (
                event.metaKey ||
                event.ctrlKey ||
                event.shiftKey ||
                event.altKey ||
                event.button !== 0
              )
                return;
              setCurrentView("preferences");
              setMenuOpen(false);
            }}
          >
            <span aria-hidden="true" className="account-avatar">
              <svg
                width="18"
                height="18"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.7"
              >
                <circle cx="12" cy="8" r="4" />
                <path d="M5 21v-2a7 7 0 0 1 14 0v2" />
              </svg>
            </span>
            <span className="max-w-40 truncate compact:hidden">
              {me.user.username ? "@" + me.user.username : me.user.display_name}
            </span>
          </a>
          <button
            className={buttonStyles.text + " ml-2 mobile:ml-0"}
            onClick={() => void logout()}
          >
            {translate("Выйти")}
          </button>
        </div>
      </header>
      {project && (
        <aside className="sidebar">
          <p className="nav-caption">{translate("Проект")}</p>
          <nav
            id="project-navigation"
            aria-label={translate("Разделы проекта")}
            className={`project-navigation ${menuOpen ? "" : "mobile:hidden"}`}
          >
            {project &&
              navigation.map((item) => (
                <a
                  key={item.view}
                  href={`#view=${item.view}&project=${selected}`}
                  aria-current={view === item.view ? "page" : undefined}
                  className={`nav-link ${view === item.view ? "bg-accent-soft text-accent font-extrabold" : "text-muted hover:bg-canvas hover:text-accent"}`}
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
        </aside>
      )}
      <main
        id="workspace-main"
        ref={main}
        tabIndex={-1}
        className="main workspace-main"
      >
        <div className="workspace-content">
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
            <section className="mx-auto grid min-h-[calc(100dvh-142px)] w-full max-w-5xl grid-cols-2 items-center gap-12 compact:grid-cols-1 compact:content-center compact:gap-6 mobile:min-h-0 mobile:py-6">
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
