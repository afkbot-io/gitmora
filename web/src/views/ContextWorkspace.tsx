import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { t } from "../domain/locale.js";
import { LatestRequest } from "../application/latest.js";
import {
  isMergeRequestURL,
  readableError,
  searchQuery,
  sourceNames,
} from "../domain/context.js";
import type {
  Hit,
  Me,
  Project,
  Repository,
  SearchResult,
  SearchQuery,
} from "../domain/context.js";
import type {
  BrowseCursor,
  BrowseEntry,
  BrowsePage,
  Selection,
} from "../domain/history.js";
import { browseQuery, navigateEntry, packQuery } from "../domain/history.js";
import { ApiClient, ApiError } from "../transport/api.js";
import { ErrorNotice, date, aborted } from "./shared.js";
import { Evidence } from "./Evidence.js";
import { ContextDetail } from "./history/ContextDetail.js";
import { buttonStyles } from "./ui.js";
import { Icon } from "./Icon.js";
import { SourceMetadata } from "./SourceMetadata.js";
import { SearchFilters } from "./SearchFilters.js";

function selectionFromHash(project: string): Selection | null {
  const p = new URLSearchParams(location.hash.slice(1));
  if (p.get("project") !== project) return null;
  const repository_id = p.get("repository") ?? undefined;
  const session_id = p.get("session") ?? undefined;
  const commit_sha = p.get("commit") ?? undefined;
  const mr = p.get("mr");
  if (!session_id && !commit_sha && !mr) return null;
  try {
    return packQuery({
      ...(p.get("dialogue") ? { dialogue_id: p.get("dialogue")! } : {}),
      project_id: project,
      ...(repository_id ? { repository_id } : {}),
      ...(session_id ? { session_id } : {}),
      ...(commit_sha ? { commit_sha } : {}),
      ...(mr ? { merge_request_iid: Number(mr) } : {}),
      ...(p.get("head") ? { expected_head_sha: p.get("head")! } : {}),
    });
  } catch {
    return null;
  }
}
export function ContextWorkspace({
  client,
  project,
  repositories,
  me,
}: {
  client: ApiClient;
  project: Project;
  repositories: Repository[];
  me: Me;
}) {
  const [text, setText] = useState("");
  const [repository, setRepository] = useState("");
  const [kind, setKind] = useState("");
  const [history, setHistory] = useState(false);
  const [filters, setFilters] = useState({
    agent: "",
    model: "",
    role: "",
    file_path: "",
  });
  const [submitted, setSubmitted] = useState<SearchQuery | null>(null);
  const inspector = useRef<HTMLElement>(null);
  const [result, setResult] = useState<SearchResult | null>(null);
  const [hit, setHit] = useState<Hit | null>(null);
  const [selection, setSelection] = useState<Selection | null>(() =>
    selectionFromHash(project.id),
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  useEffect(() => () => requests.cancel(), [requests]);
  useEffect(() => {
    const read = () => setSelection(selectionFromHash(project.id));
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, [project.id]);
  function open(value: Selection, source?: Hit, fromMR?: number) {
    const q = packQuery(value);
    if (q.project_id !== project.id) return;
    const p = new URLSearchParams({ view: "context", project: project.id });
    if (q.repository_id) p.set("repository", q.repository_id);
    if (q.session_id) {
      p.set("session", q.session_id);
      if (source?.source.session_id === q.session_id) {
        const match = /^messages\/(\d+)$/.exec(source.part);
        if (match) p.set("message", match[1]!);
        if (source.source.revision)
          p.set("revision", String(source.source.revision));
      }
    }
    if (q.dialogue_id) p.set("dialogue", q.dialogue_id);
    if (fromMR) p.set("from_mr", String(fromMR));
    if (q.commit_sha) p.set("commit", q.commit_sha);
    if (q.merge_request_iid) p.set("mr", String(q.merge_request_iid));
    if (q.expected_head_sha) p.set("head", q.expected_head_sha);
    location.hash = p.toString();
  }
  function clearResults() {
    requests.cancel();
    setResult(null);
    setSubmitted(null);
    setHit(null);
    setError("");
    setLoading(false);
  }
  async function search(event: FormEvent) {
    event.preventDefault();
    clearResults();
    const request = requests.begin();
    setLoading(true);
    try {
      if (isMergeRequestURL(text)) {
        const resolved = await client.request<{ query: Selection }>(
          "/v1/context/resolve",
          { project_id: project.id, url: text.trim() },
          request.signal,
        );
        if (!request.current()) return;
        if (
          resolved.query.project_id !== project.id ||
          (repository && resolved.query.repository_id !== repository)
        )
          throw new Error(
            "Ссылка относится к другому репозиторию. Смените фильтр.",
          );
        open(resolved.query);
        return;
      }
      const query = searchQuery(
        project.id,
        repository,
        text,
        kind,
        history,
        filters,
      );
      const found = await client.request<SearchResult>(
        "/v1/context/search",
        query,
        request.signal,
      );
      if (!request.current()) return;
      if (found.items.some((item) => item.source.project_id !== project.id))
        throw new Error("Сервер вернул источники другого проекта.");
      setSubmitted(query);
      setResult(found);
      setHit(found.items[0] ?? null);
    } catch (e) {
      if (!aborted(e) && request.current())
        setError(
          e instanceof ApiError && e.code === "ambiguous_commit"
            ? t("Уточните SHA: найдено несколько коммитов.")
            : e instanceof ApiError &&
                e.status === 404 &&
                /^[0-9a-f]{7,64}$/i.test(text.trim())
              ? t("Коммит не найден.")
              : e instanceof ApiError &&
                  e.status === 404 &&
                  isMergeRequestURL(text)
                ? t(
                    "MR/PR ещё не синхронизирован. Проверьте подключение репозитория.",
                  )
                : readableError(e),
        );
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  async function more() {
    if (loading || !submitted || !result?.next) return;
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const found = await client.request<SearchResult>(
        "/v1/context/search",
        { ...submitted, after: result.next },
        request.signal,
      );
      if (!request.current()) return;
      if (
        found.items.some(
          (item) =>
            item.source.project_id !== project.id ||
            (submitted.repository_id &&
              item.source.repository_id !== submitted.repository_id),
        )
      )
        throw new Error("Сервер вернул источники другого проекта.");
      setResult((previous) => {
        const seen = new Set(previous?.items.map((item) => item.id));
        return {
          ...found,
          items: [
            ...(previous?.items ?? []),
            ...found.items.filter((item) => !seen.has(item.id)),
          ],
        };
      });
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  if (selection)
    return (
      <ContextDetail
        key={JSON.stringify(selection)}
        client={client}
        selection={selection}
        provider={
          repositories.find((r) => r.id === selection.repository_id)
            ?.provider === "github"
            ? "github"
            : "gitlab"
        }
        onNavigate={(q, fromMR) => open(q, undefined, fromMR)}
        onBack={() => {
          location.hash = new URLSearchParams({
            view: "context",
            project: project.id,
          }).toString();
        }}
      />
    );
  return (
    <section className="context-view min-w-0">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="mb-1">{t("Поиск по истории")}</h1>
          <p className="mb-0 text-sm text-muted">
            {t("Сессии агентов, коммиты и код проекта.")}
          </p>
        </div>
      </div>
      <form
        className="search-form rounded-lg border border-line bg-paper p-4"
        onSubmit={(e) => void search(e)}
      >
        <label className="sr-only" htmlFor="search">
          {t("Запрос или ссылка на MR/PR")}
        </label>
        <div className="search-line flex gap-3 mobile:flex-col">
          <div className="relative min-w-0 flex-1">
            <span className="pointer-events-none absolute left-3 top-3 text-muted">
              <Icon name="context" />
            </span>
            <input
              id="search"
              className="w-full pl-10 pr-3"
              type="search"
              autoComplete="off"
              value={text}
              maxLength={2000}
              placeholder={t("Текст, SHA, UUID сессии или ссылка на PR…")}
              onChange={(e) => {
                setText(e.target.value);
                clearResults();
              }}
            />
          </div>
          <button
            className={buttonStyles.primary + " min-w-28"}
            disabled={
              loading ||
              (!text.trim() &&
                !Object.values(filters).some((value) => value.trim()))
            }
          >
            {loading ? t("Ищем…") : t("Найти")}
          </button>
        </div>
        <div className="filters mt-3 flex flex-wrap items-center gap-3 border-t border-line pt-3 mobile:grid mobile:grid-cols-2">
          <label className="m-0">
            <span className="sr-only">{t("Репозиторий")}</span>
            <select
              className="min-w-45 text-sm mobile:w-full mobile:min-w-0"
              aria-label={t("Репозиторий")}
              value={repository}
              onChange={(e) => {
                setRepository(e.target.value);
                if (!e.target.value)
                  setFilters((current) => ({ ...current, file_path: "" }));
                clearResults();
              }}
            >
              <option value="">{t("Все репозитории")}</option>
              {repositories.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
          <label className="m-0">
            <span className="sr-only">{t("Источник")}</span>
            <select
              className="min-w-40 text-sm mobile:w-full mobile:min-w-0"
              aria-label={t("Источник")}
              value={kind}
              onChange={(e) => {
                setKind(e.target.value);
                clearResults();
              }}
            >
              <option value="">{t("Все источники")}</option>
              {Object.entries(sourceNames).map(([value, label]) => (
                <option key={value} value={value}>
                  {t(label)}
                </option>
              ))}
            </select>
          </label>
          <label className="checkbox m-0 ml-auto flex min-h-11 items-center gap-2 text-sm font-normal text-muted mobile:col-span-2 mobile:ml-0">
            <input
              type="checkbox"
              checked={history}
              onChange={(e) => {
                setHistory(e.target.checked);
                clearResults();
              }}
            />
            {t("Исторические версии")}
          </label>
        </div>
        <SearchFilters
          value={filters}
          repository={repository}
          onChange={(value) => {
            setFilters(value);
            clearResults();
          }}
        />
      </form>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && (
        <p className="mt-4 text-sm text-muted" role="status">
          {t("Поиск…")}
        </p>
      )}
      {result ? (
        <div className="mt-5">
          <div
            className="mb-3 flex flex-wrap items-center justify-between gap-2 text-sm text-muted"
            role="status"
          >
            <span>{t("Результаты: {0}", result.items.length)}</span>
            {result.semantic_unavailable ? (
              <span>{t("Поиск по словам")}</span>
            ) : (
              (result.pending_sources_in_project > 0 ||
                (result.semantic_pending ?? 0) > 0) && (
                <span>{t("Индекс обновляется")}</span>
              )
            )}
          </div>
          {!result.items.length ? (
            <div className="rounded-lg border border-line bg-paper p-6">
              <h2 className="mb-2 text-base">{t("Ничего не найдено")}</h2>
              <p className="mb-0 text-sm text-muted">
                {t("Попробуйте другие слова или полный SHA коммита.")}
              </p>
            </div>
          ) : (
            <div className="evidence-layout grid grid-cols-[minmax(0,1fr)_minmax(340px,1fr)] overflow-hidden rounded-lg border border-line bg-paper compact:grid-cols-1">
              <div
                className="results min-w-0"
                role="region"
                aria-label={t("Результаты поиска")}
              >
                {result.items.map((item) => (
                  <button
                    key={item.id}
                    className={`result-row block w-full rounded-none border-0 border-b border-l-2 border-b-line px-4 py-4 text-left font-normal ${hit?.id === item.id ? "selected border-l-accent bg-accent-soft" : "border-l-transparent bg-paper hover:bg-canvas"}`}
                    aria-pressed={hit?.id === item.id}
                    onClick={() => {
                      setHit(item);
                      const panel = inspector.current;
                      const layout = panel?.parentElement;
                      if (
                        panel &&
                        layout &&
                        getComputedStyle(layout)
                          .gridTemplateColumns.trim()
                          .split(/\s+/).length === 1
                      ) {
                        panel.scrollIntoView({
                          block: "start",
                          behavior: "instant",
                        });
                        panel.focus({ preventScroll: true });
                      }
                    }}
                  >
                    <span className="mb-2 flex flex-wrap justify-between gap-2 text-xs text-muted">
                      <span>
                        {item.metadata?.agent ? (
                          <SourceMetadata metadata={item.metadata} />
                        ) : (
                          t(sourceNames[item.kind] ?? item.kind)
                        )}
                      </span>
                      <span>{date(item.observed_at)}</span>
                    </span>
                    <span className="mb-2 block wrap-anywhere text-sm font-medium">
                      {item.title}
                    </span>
                    <span className="block max-h-24 overflow-hidden whitespace-pre-wrap wrap-anywhere text-sm leading-relaxed text-muted">
                      {item.content}
                    </span>
                  </button>
                ))}
                {result.next && (
                  <div className="p-4">
                    <button
                      className={buttonStyles.secondary + " w-full"}
                      disabled={loading}
                      onClick={() => void more()}
                    >
                      {loading ? t("Загрузка…") : t("Показать ещё")}
                    </button>
                  </div>
                )}
              </div>
              <aside
                ref={inspector}
                tabIndex={-1}
                className="inspector min-w-0 border-l border-line compact:border-l-0 compact:border-t"
                aria-label={t("Точный источник")}
              >
                {hit && (
                  <>
                    <div className="flex justify-end border-b border-line px-4 py-2">
                      <button
                        className={buttonStyles.text}
                        onClick={() => {
                          if (hit.source.repository_id && hit.source.session_id)
                            open(
                              {
                                project_id: project.id,
                                repository_id: hit.source.repository_id,
                                session_id: hit.source.session_id,
                              },
                              hit,
                            );
                          else if (
                            hit.source.repository_id &&
                            hit.source.commit_sha
                          )
                            open({
                              project_id: project.id,
                              repository_id: hit.source.repository_id,
                              commit_sha: hit.source.commit_sha,
                            });
                        }}
                      >
                        {t("Открыть контекст")}
                      </button>
                    </div>
                    <Evidence
                      key={hit.id}
                      client={client}
                      hit={hit}
                      onOpen={(source) => {
                        if (
                          source.source.repository_id &&
                          source.source.session_id
                        )
                          open(
                            {
                              project_id: project.id,
                              repository_id: source.source.repository_id,
                              session_id: source.source.session_id,
                            },
                            source,
                          );
                      }}
                    />
                  </>
                )}
              </aside>
            </div>
          )}
        </div>
      ) : (
        !loading && (
          <div className="mt-6 grid grid-cols-2 items-start gap-5 compact:grid-cols-1">
            {me.scopes.includes("capture:read") && (
              <RecentSources
                key={`session/${repository}`}
                client={client}
                project={project.id}
                repository={repository}
                repositories={repositories}
                kind="session"
                onOpen={open}
              />
            )}
            {me.scopes.includes("integration:read") && (
              <RecentSources
                key={`mr/${repository}`}
                client={client}
                project={project.id}
                repository={repository}
                repositories={repositories}
                kind="merge_request"
                onOpen={open}
              />
            )}
          </div>
        )
      )}
    </section>
  );
}

function RecentSources({
  client,
  project,
  repository,
  repositories,
  kind,
  onOpen,
}: {
  client: ApiClient;
  project: string;
  repository: string;
  repositories: Repository[];
  kind: "session" | "merge_request";
  onOpen: (q: Selection) => void;
}) {
  const [items, setItems] = useState<BrowseEntry[]>([]);
  const [next, setNext] = useState<BrowseCursor | undefined>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [requests] = useState(() => new LatestRequest());
  async function load(after?: BrowseCursor) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    if (!after) setItems([]);
    try {
      const page = await client.request<BrowsePage>(
        "/v1/context/browse",
        {
          ...browseQuery(project, kind, repository, after),
          order: "recent",
          limit: 6,
        },
        request.signal,
      );
      if (!request.current()) return;
      if (page.items.some((item) => item.query.project_id !== project))
        throw new Error("Сервер вернул источники другого проекта.");
      setItems((current) => (after ? [...current, ...page.items] : page.items));
      setNext(page.next);
    } catch (e) {
      if (!aborted(e) && request.current()) {
        setItems([]);
        setNext(undefined);
        setError(readableError(e));
      }
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, project, repository, requests]);
  return (
    <section className="min-w-0 overflow-hidden rounded-lg border border-line bg-paper">
      <div className="flex items-center justify-between border-b border-line px-4 py-3">
        <h2 className="m-0 text-base">
          {kind === "session" ? t("Недавние сессии") : t("MR и PR")}
        </h2>
        <button
          className={buttonStyles.text}
          disabled={loading}
          onClick={() => void load()}
        >
          {t("Обновить")}
        </button>
      </div>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && !items.length && (
        <p className="m-0 p-5 text-sm text-muted" role="status">
          {t("Загрузка…")}
        </p>
      )}
      {!loading && !error && !items.length && (
        <div className="p-5">
          <p className="mb-3 text-sm text-muted">
            {kind === "session"
              ? t("Подключите CLI и запишите первую сессию агента.")
              : t("MR/PR появятся после синхронизации репозитория.")}
          </p>
          <a
            className={buttonStyles.text}
            href={`#view=${kind === "session" ? "setup" : "repositories"}&project=${project}`}
          >
            {kind === "session"
              ? t("Настроить CLI и MCP")
              : t("Проверить подключение")}
          </a>
        </div>
      )}
      {items.map((item) => (
        <button
          key={`${item.query.repository_id}/${item.query.session_id ?? item.cursor.iid}`}
          className="flex w-full flex-col items-start gap-1 rounded-none border-0 border-b border-line px-4 py-3 text-left font-normal hover:bg-canvas"
          onClick={() => {
            try {
              onOpen(navigateEntry(item, project));
            } catch (e) {
              setError(readableError(e));
            }
          }}
        >
          <span className="w-full truncate text-sm font-medium">
            {item.title}
          </span>
          <span className="flex w-full flex-wrap justify-between gap-2 text-xs text-muted">
            <span className="min-w-0 truncate">
              {
                repositories.find((r) => r.id === item.query.repository_id)
                  ?.name
              }
              {kind === "merge_request"
                ? ` · #${item.cursor.iid}`
                : ` · ${item.agent}`}
            </span>
            <span>{date(item.observed_at)}</span>
          </span>
        </button>
      ))}
      {next && (
        <div className="px-4 py-2">
          <button
            className={buttonStyles.text}
            disabled={loading}
            onClick={() => void load(next)}
          >
            {t("Показать ещё")}
          </button>
        </div>
      )}
    </section>
  );
}
