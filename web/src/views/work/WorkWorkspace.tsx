import { t as translate } from "../../domain/locale.js";
import { buttonStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { Me, Project, Repository } from "../../domain/context.js";
import { readableError } from "../../domain/context.js";
import type {
  BrowseCursor,
  BrowseEntry,
  BrowsePage,
  Selection,
  WorkKind,
} from "../../domain/work.js";
import {
  browseQuery,
  navigateEntry,
  packQuery,
  sameSelection,
} from "../../domain/work.js";
import { LatestRequest } from "../../application/latest.js";
import type { ApiClient } from "../../transport/api.js";
import { Empty, ErrorNotice, aborted, date } from "../shared.js";
import { WorkDetail } from "./WorkDetail.js";

const names: Record<WorkKind, string> = {
  task: "Задачи Tasktopia",
  session: "Сессии агента",
  merge_request: "Merge requests",
};

/** Browse accepted source observations, then navigate only through their explicit selectors. */
export function WorkWorkspace({
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
  const kinds: WorkKind[] = [];
  if (me.scopes.includes("integration:read"))
    kinds.push("task", "merge_request");
  if (me.scopes.includes("capture:read")) kinds.push("session");
  const [kind, setKind] = useState<WorkKind>(kinds[0] ?? "task");
  const [repository, setRepository] = useState("");
  const [items, setItems] = useState<BrowseEntry[]>([]);
  const [next, setNext] = useState<BrowseCursor | undefined>();
  const [readAt, setReadAt] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selection, setSelection] = useState<Selection | null>(null);
  const [history, setHistory] = useState<Selection[]>([]);
  const [requests] = useState(() => new LatestRequest());
  function clear() {
    requests.cancel();
    setItems([]);
    setNext(undefined);
    setReadAt("");
    setSelection(null);
    setHistory([]);
    setError("");
  }
  async function load(after?: BrowseCursor) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    if (!after) {
      setItems([]);
      setNext(undefined);
      setSelection(null);
      setHistory([]);
    }
    try {
      const result = await client.request<BrowsePage>(
        "/v1/context/browse",
        browseQuery(project.id, kind, repository, after),
        request.signal,
      );
      if (!request.current()) return;
      if (
        result.schema_version !== 1 ||
        result.query.project_id !== project.id ||
        result.query.kind !== kind ||
        (result.query.repository_id ?? "") !== repository ||
        result.items.some(
          (item) =>
            item.query.project_id !== project.id ||
            (repository && item.query.repository_id !== repository),
        )
      )
        throw new Error("Список не совпал с выбранной областью.");
      setItems((current) =>
        after ? [...current, ...result.items] : result.items,
      );
      setNext(result.next);
      setReadAt(result.read_at);
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    if (kinds.length) void load();
    return () => requests.cancel();
  }, [client, project.id, kind, repository, requests]);
  function navigate(q: Selection) {
    try {
      packQuery(q);
      if (q.project_id !== project.id)
        throw new Error("Источник относится к другому проекту.");
      if (selection && !sameSelection(selection, q))
        setHistory((current) => [...current.slice(-19), selection]);
      setSelection(q);
      setError("");
    } catch (e) {
      setError(readableError(e));
    }
  }
  if (!kinds.length)
    return (
      <Empty title={translate("Нет доступа к рабочим источникам")}>
        {translate(
          "Нужно capture:read для сессий или integration:read для задач и MR.",
        )}
      </Empty>
    );
  return (
    <section className="work-view">
      <div className="section-heading flex justify-between items-start gap-4 flex-wrap [&_h1]:mb-2 [&_h2]:mb-2 [&_>_div]:min-w-0 [&_>_div]:max-w-full">
        <div>
          <h1>{translate("Задачи и история работы")}</h1>
        </div>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {translate("Обновить источники")}
        </button>
      </div>
      <div
        className={`
        filters work-filters flex gap-4 items-end flex-wrap [&_label]:text-xs [&_label]:text-muted
        [&_label]:font-normal [&_label]:m-0 [&_select]:block [&_select]:text-sm [&_select]:mt-1
        [&_select]:min-w-45 [&_select]:min-h-11 [&_select]:px-3 [&_select]:py-2 [&_.checkbox]:flex
        [&_.checkbox]:gap-2 [&_.checkbox]:items-center [&_.checkbox]:text-sm [&_.checkbox]:min-h-11
        compact:gap-3 compact:[&_select]:min-w-40
        mobile:grid mobile:grid-cols-[1fr_1fr]
        mobile:gap-3 mobile:[&_select]:min-w-0
        mobile:[&_select]:w-full mobile:[&_select]:text-base
        mobile:[&_.checkbox]:[grid-column:1/-1] mx-0 mt-5 mb-3
      `}
      >
        <label>
          {translate("Вид источника")}
          <select
            aria-label={translate("Вид источника")}
            value={kind}
            onChange={(e) => {
              clear();
              setRepository("");
              setKind(e.target.value as WorkKind);
            }}
          >
            {kinds.map((k) => (
              <option value={k} key={k}>
                {translate(names[k])}
              </option>
            ))}
          </select>
        </label>
        {kind !== "task" && (
          <label>
            {translate("Репозиторий источника")}
            <select
              aria-label={translate("Репозиторий источника")}
              value={repository}
              onChange={(e) => {
                clear();
                setRepository(e.target.value);
              }}
            >
              <option value="">{translate("Все репозитории")}</option>
              {repositories.map((r) => (
                <option value={r.id} key={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <p className="small muted text-muted text-sm">
        {translate(
          "Показаны принятые локальные записи в порядке идентификаторов.",
        )}{" "}
        {kind === "session"
          ? translate(
              "Каждая строка — сохранённое окно сессии; проверенная цепочка открывается в деталях.",
            )
          : kind === "task"
            ? translate(
                "Задачи обнаруживаются по привязкам или явной синхронизации проекта.",
              )
            : translate(
                "Доступность MR/PR зависит от сверки репозитория.",
              )}{" "}
        {translate(
          "Пустой список не означает отсутствие работы во внешнем сервисе.",
        )}
      </p>
      <ErrorNotice>{error}</ErrorNotice>
      {error && (
        <button className={buttonStyles.secondary} onClick={() => void load()}>
          {translate("Повторить список источников")}
        </button>
      )}
      {loading && (
        <p role="status" className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate("Читаем доступные источники…")}
        </p>
      )}
      {readAt && (
        <p className="small muted text-muted text-sm">
          {translate("Список прочитан")} {date(readAt)}
          {translate(". Изменения источников появятся при обновлении.")}
        </p>
      )}
      <div
        className={`
        work-layout grid grid-cols-[minmax(220px,_0.7fr)_minmax(0,_1.8fr)] gap-7 items-start mt-6
        compact:grid-cols-[minmax(0,_1fr)]
      `}
      >
        <div
          className="work-list min-w-0 max-h-[72vh] overflow-auto pr-1 compact:max-h-105"
          role="region"
          aria-busy={loading}
          aria-label={translate("Рабочие источники")}
        >
          {items.map((item) => (
            <button
              className={`work-row flex flex-col items-stretch gap-1 w-full text-left rounded-none border-b border-line px-3 py-4 bg-transparent text-ink font-normal wrap-anywhere [&.selected]:bg-accent-soft [&.selected]:[box-shadow:inset_3px_0_var(--color-accent)] [&:hover:not(.selected)]:bg-canvas ${selection && sameSelection(selection, item.query) ? "selected" : ""}`}
              key={JSON.stringify(item.cursor)}
              aria-pressed={!!selection && sameSelection(selection, item.query)}
              onClick={() => {
                try {
                  navigate(navigateEntry(item, project.id));
                } catch (e) {
                  setError(readableError(e));
                }
              }}
            >
              <strong>
                {item.cursor.iid
                  ? `!${item.cursor.iid} `
                  : item.number
                    ? `#${item.number} `
                    : ""}
                {item.title}
              </strong>
              {item.state && <span>{item.state}</span>}
              {item.revision && (
                <span>
                  {translate("Окно")} {item.segment}
                  {translate(", ревизия")} {item.revision}
                </span>
              )}
              <span className="small text-sm">
                {translate("Наблюдение:")} {date(item.observed_at)}
              </span>
              <span className="mono small font-mono text-xs wrap-anywhere">
                {item.cursor.id ?? item.cursor.repository_id}
              </span>
            </button>
          ))}
          {!items.length && !loading && !error && (
            <Empty title={translate("Сохранённых источников пока нет")}>
              {translate(
                "Проверьте выбранный вид, фильтр и состояние интеграций.",
              )}
            </Empty>
          )}
          {next && (
            <button
              className={buttonStyles.secondary}
              disabled={loading}
              onClick={() => void load(next)}
            >
              {translate("Загрузить ещё источники")}
            </button>
          )}
        </div>
        <div
          className="work-inspector min-w-0"
          role="region"
          aria-label={translate("Контекст работы")}
        >
          {history.length > 0 && (
            <button
              className="secondary work-back bg-paper text-ink border-control mb-4"
              onClick={() => {
                setSelection(history[history.length - 1]!);
                setHistory((current) => current.slice(0, -1));
              }}
            >
              {translate("Назад к источнику")}
            </button>
          )}
          {selection ? (
            <WorkDetail
              key={JSON.stringify(selection)}
              client={client}
              selection={selection}
              onNavigate={navigate}
            />
          ) : (
            <Empty title={translate("Откройте источник")}>
              {translate(
                "Здесь появятся задача, окна сессии, подтверждённые изменения и точные цитаты.",
              )}
            </Empty>
          )}
        </div>
      </div>
    </section>
  );
}
