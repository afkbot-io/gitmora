import { t as translate } from "../domain/locale.js";
import { workspaceView, type WorkspaceView } from "../domain/navigation.js";
import { Preferences } from "./Preferences.js";
import { Icon } from "./Icon.js";
import { buttonStyles } from "./ui.js";
import { useEffect, useRef, useState } from "react";
import { LatestRequest } from "../application/latest.js";
import { readableError } from "../domain/context.js";
import { capabilities } from "../domain/settings.js";
import type { AuthConfig, Me, Page, Project } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { Brand, ErrorNotice, Empty, aborted } from "./shared.js";
import { ProjectCatalog } from "./Catalog.js";
import { Accounts } from "./Accounts.js";
import { ProjectWorkspace } from "./ProjectWorkspace.js";
import { Keys } from "./credentials/Keys.js";
import { Bindings } from "./credentials/Bindings.js";
import { SetupGuide } from "./AgentSetup.js";

type View = WorkspaceView;

function catalogReadableFor(me: Me) {
  return me.scopes.includes("directory:read");
}

export function Workspace({
  client,
  me,
  logout,
  onMe,
  authConfig,
  notice,
  clearNotice,
}: {
  client: ApiClient;
  me: Me;
  logout: () => Promise<void>;
  onMe: (value: Me) => void;
  authConfig: AuthConfig;
  notice: string;
  clearNotice: () => void;
}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState("");
  const selection = useRef("");
  function choose(id: string) {
    selection.current = id;
    setSelected(id);
  }
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const fallbackView =
    !catalogReadableFor(me) && me.scopes.includes("credentials:manage")
      ? "keys"
      : "context";
  const [view, setCurrentView] = useState<View>(() =>
    workspaceView(location.hash, fallbackView),
  );
  useEffect(() => {
    const read = () =>
      setCurrentView(workspaceView(location.hash, fallbackView));
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, [fallbackView]);
  function setView(target: View) {
    setCurrentView(target);
    if (location.hash.startsWith("#view="))
      history.replaceState(null, "", "#view=" + target);
  }
  const [menuOpen, setMenuOpen] = useState(false);
  const focusContent = useRef(false);
  const main = useRef<HTMLElement>(null);
  function navigate(target: View) {
    focusContent.current = menuOpen;
    setCurrentView(target);
    setMenuOpen(false);
  }
  useEffect(() => {
    if (focusContent.current) {
      main.current?.focus();
      focusContent.current = false;
    }
  }, [view, menuOpen]);
  const [requests] = useState(() => new LatestRequest());
  const project = projects.find((item) => item.id === selected);
  const rights = capabilities(me, project);
  const catalogReadable = me.scopes.includes("directory:read");
  const keysAllowed = me.scopes.includes("credentials:manage");
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
      if (!after)
        choose(
          (page.items.find((item) => item.role) ?? page.items[0])?.id ?? "",
        );
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  async function openProject(id: string, target: View = "context") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const value = await client.request<Project>(
        `/v1/projects/${id}`,
        undefined,
        request.signal,
      );
      if (!request.current()) return;
      if (value.id !== id)
        throw new Error("Ответ не совпал с выбранным проектом.");
      setProjects((current) =>
        current.some((item) => item.id === id)
          ? current.map((item) => (item.id === id ? value : item))
          : [...current, value],
      );
      choose(id);
      setView(target);
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  async function refreshProject(deleted = false) {
    if (!project || selection.current !== project.id) return;
    if (deleted) {
      setProjects((current) =>
        current.filter((item) => item.id !== project.id),
      );
      choose("");
      setView("catalog");
      await load();
    } else {
      const id = project.id;
      const request = requests.begin();
      setLoading(true);
      setError("");
      try {
        const value = await client.request<Project>(
          `/v1/projects/${id}`,
          undefined,
          request.signal,
        );
        if (request.current() && selection.current === id) {
          if (value.id !== id)
            throw new Error("Ответ не совпал с выбранным проектом.");
          setProjects((current) =>
            current.map((item) => (item.id === id ? value : item)),
          );
        }
      } catch (e) {
        if (!aborted(e) && request.current()) setError(readableError(e));
      } finally {
        if (request.current()) setLoading(false);
      }
    }
  }
  async function refreshMe() {
    try {
      onMe(await client.request<Me>("/v1/me"));
    } catch (e) {
      if (!aborted(e)) {
        setError(readableError(e));
        setView("context");
      }
    }
  }
  useEffect(() => {
    if (catalogReadable) void load();
    return () => requests.cancel();
  }, [client, requests, catalogReadable]);
  return (
    <div
      className={`
      workspace grid grid-cols-[15rem_minmax(0,_1fr)] min-h-[100dvh] max-w-480 m-auto
      compact:grid-cols-[14rem_minmax(0,_1fr)] mobile:block
    `}
    >
      <a
        className="skip absolute -top-25 left-64 z-10 bg-paper p-3 [&:focus]:top-3 mobile:left-4"
        href="#workspace-main"
      >
        {translate("К содержимому")}
      </a>
      <aside
        className={`
        sidebar bg-paper border-r border-line px-6 pt-8 pb-6 flex flex-col gap-9
        min-w-0 [&_.brand]:text-2xl [&_nav_a]:justify-start
        [&_nav_a]:bg-transparent [&_nav_a]:text-muted [&_nav_a]:px-3 [&_nav_a]:py-2
        [&_nav_a]:rounded-lg
 [&_nav_a[aria-current]]:bg-accent-soft
        [&_nav_a[aria-current]]:text-accent compact:px-4
        mobile:border-r-0
        mobile:border-b border-line mobile:p-4
        mobile:gap-4 mobile:grid
        mobile:grid-cols-[1fr_1fr] mobile:items-start
        mobile:[&_.brand]:text-xl

        mobile:[&_nav_a]:justify-center mobile:[&_nav_a]:min-h-11
        mobile:[&_nav_a]:p-2
        mobile:[&_nav_a]:[flex:1_1_42%] mobile:[&_nav_a]:text-sm
        [&_nav_a]:text-left [&_nav_a:not([aria-current]):hover]:bg-canvas
      `}
      >
        <Brand />
        {catalogReadable && projects.length > 0 && (
          <div
            className={`
          project-select [&_select]:w-full [&_select]:font-medium mobile:[grid-column:1/-1]
          mobile:[grid-row:2] mobile:[&_label]:sr-only
        `}
          >
            <label htmlFor="project">{translate("Проект")}</label>
            <select
              id="project"
              value={selected}
              onChange={(e) => {
                requests.cancel();
                setLoading(false);
                choose(e.target.value);
                if (view === "catalog" || view === "accounts")
                  setView("context");
              }}
              disabled={loading || projects.length === 0}
            >
              {!projects.length && (
                <option value="">{translate("Нет проектов")}</option>
              )}
              {projects.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
            {next && (
              <button
                className={buttonStyles.text}
                disabled={loading}
                onClick={() => void load(next)}
              >
                {translate("Загрузить ещё проекты")}
              </button>
            )}
          </div>
        )}
        <button
          type="button"
          className="mobile-menu hidden mobile:inline-flex mobile:col-span-full bg-canvas text-ink border-control justify-between"
          aria-expanded={menuOpen}
          aria-controls="project-navigation"
          onClick={() => setMenuOpen(!menuOpen)}
        >
          {translate("Разделы")}
          <svg
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.7"
            aria-hidden="true"
          >
            <path d={menuOpen ? "m6 15 6-6 6 6" : "m6 9 6 6 6-6"} />
          </svg>
        </button>
        <nav
          id="project-navigation"
          aria-label={translate("Разделы проекта")}
          className={`flex flex-col gap-2 mobile:col-span-full mobile:flex-row mobile:flex-wrap ${menuOpen ? "" : "mobile:hidden"}`}
        >
          {(
            [
              { view: "work", label: translate("Работа"), visible: !!project },
              {
                view: "context",
                label: translate("Контекст"),
                visible: !!project || catalogReadable,
              },
              {
                view: "repositories",
                label: translate("Репозитории"),
                visible: !!project,
              },
              {
                view: "settings",
                label: translate("Настройки проекта"),
                visible: !!project,
              },
              {
                view: "catalog",
                label: translate("Проекты"),
                visible: catalogReadable,
              },
              {
                view: "accounts",
                label: translate("Учётные записи"),
                visible: rights.accounts,
              },
              {
                view: "bindings",
                label: translate("Привязки входа"),
                visible: rights.accounts,
              },
              {
                view: "keys",
                label: translate("Личные ключи"),
                visible: keysAllowed,
              },
              { view: "setup", label: translate("Подключение"), visible: true },
            ] satisfies { view: View; label: string; visible: boolean }[]
          )
            .filter((item) => item.visible)
            .map((item) => (
              <a
                key={item.view}
                href={"#view=" + item.view}
                className="action-link"
                aria-current={view === item.view ? "page" : undefined}
                onClick={(event) => {
                  if (
                    !event.metaKey &&
                    !event.ctrlKey &&
                    !event.shiftKey &&
                    !event.altKey &&
                    event.button === 0
                  )
                    navigate(item.view);
                }}
              >
                <Icon name={item.view} />
                {item.label}
              </a>
            ))}
        </nav>
        <div
          className={`
          account mt-auto border-t border-line pt-5 flex flex-col gap-2 wrap-anywhere
          [&_strong]:text-sm [&_strong]:font-medium [&_span]:text-xs [&_span]:text-muted
          mobile:[grid-column:2] mobile:[grid-row:1]
          mobile:border-0 mobile:m-0 mobile:p-0
          mobile:text-right mobile:gap-0 mobile:[&_span]:hidden
          mobile:[&_button]:justify-end mobile:[&_button]:min-h-[26px]
        `}
        >
          <a
            href="#view=preferences"
            className="text-sm font-medium no-underline hover:text-accent"
            aria-current={view === "preferences" ? "page" : undefined}
            onClick={() => navigate("preferences")}
          >
            {translate("Настройки")}
          </a>
          <strong>{me.user.display_name}</strong>
          <span>
            {project?.role
              ? ({
                  reader: translate("Чтение проекта"),
                  writer: translate("Участник проекта"),
                  admin: translate("Администратор проекта"),
                }[project.role] ?? project.role)
              : translate("Без роли в проекте")}
          </span>
          <button className={buttonStyles.text} onClick={() => void logout()}>
            {translate("Выйти")}
          </button>
        </div>
      </aside>
      <main
        ref={main}
        id="workspace-main"
        className={`
          main min-w-0 px-10 pt-8 pb-10 wide:px-14 compact:px-6
          mobile:px-4 mobile:pt-6 mobile:pb-6
        `}
        tabIndex={-1}
      >
        <ErrorNotice>{error}</ErrorNotice>
        {notice && (
          <div className="auth-notice mt-4 [&_.notice]:mb-1">
            <ErrorNotice>{notice}</ErrorNotice>
            <button className={buttonStyles.text} onClick={clearNotice}>
              {translate("Скрыть уведомление")}
            </button>
          </div>
        )}
        {error && (
          <button
            className={buttonStyles.secondary}
            disabled={loading}
            onClick={() => void load()}
          >
            {translate("Повторить загрузку")}
          </button>
        )}
        {view === "preferences" ? (
          <Preferences />
        ) : view === "setup" ? (
          <section className="pb-8 max-w-220">
            <h1>{translate("Подключение CLI и MCP")}</h1>
            <h2 className="text-lg mt-6 mb-3">
              {translate("На компьютере разработчика")}
            </h2>
            <SetupGuide />
          </section>
        ) : view === "keys" ? (
          keysAllowed ? (
            <Keys client={client} me={me} />
          ) : (
            <Empty
              heading="h1"
              title={translate("Нет доступа к личным ключам")}
            >
              {translate(
                "Попросите администратора предоставить доступ к управлению ключами.",
              )}
            </Empty>
          )
        ) : view === "bindings" ? (
          rights.accounts ? (
            <Bindings client={client} config={authConfig} />
          ) : (
            <Empty
              heading="h1"
              title={translate("Нет доступа к OIDC-привязкам")}
            >
              {translate("Этот раздел доступен администраторам Gitmora.")}
            </Empty>
          )
        ) : view === "catalog" ? (
          <ProjectCatalog
            client={client}
            me={me}
            projects={projects}
            onSaved={async (id) => {
              if (id) await openProject(id);
              else await load();
            }}
            next={!!next}
            loading={loading}
            loadMore={() => void load(next)}
          />
        ) : view === "accounts" ? (
          rights.accounts ? (
            <Accounts
              client={client}
              refreshMe={refreshMe}
              registrationEnabled={authConfig.registration_enabled === true}
            />
          ) : (
            <Empty
              heading="h1"
              title={translate("Нет доступа к учётным записям")}
            >
              {translate("Этот раздел доступен администраторам Gitmora.")}
            </Empty>
          )
        ) : loading && !projects.length ? (
          <p className="notice px-4 py-4 mx-0 my-3 text-muted" role="status">
            {translate("Загружаем проекты…")}
          </p>
        ) : !project ? (
          <Empty heading="h1" title={translate("Нет доступных проектов")}>
            {translate("Попросите администратора добавить вас в проект.")}
          </Empty>
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
      </main>
    </div>
  );
}
