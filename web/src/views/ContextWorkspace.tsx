import { t as translate } from "../domain/locale.js";
import { buttonStyles } from "./ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { LatestRequest } from "../application/latest.js";
import { readableError, searchQuery, sourceNames } from "../domain/context.js";
import type {
  Hit,
  Project,
  Repository,
  SearchResult,
} from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { ErrorNotice, Empty, date, aborted } from "./shared.js";
import { Evidence } from "./Evidence.js";

export function ContextWorkspace({
  client,
  project,
  repositories,
}: {
  client: ApiClient;
  project: Project;
  repositories: Repository[];
}) {
  const [text, setText] = useState("");
  const [repository, setRepository] = useState("");
  const [kind, setKind] = useState("");
  const [history, setHistory] = useState(false);
  const [result, setResult] = useState<SearchResult | null>(null);
  const [selected, setSelected] = useState<Hit | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  useEffect(() => () => requests.cancel(), [requests]);
  function clearResults() {
    requests.cancel();
    setResult(null);
    setSelected(null);
    setError("");
    setLoading(false);
  }
  async function search(event: FormEvent) {
    event.preventDefault();
    clearResults();
    const request = requests.begin();
    try {
      const query = searchQuery(project.id, repository, text, kind, history);
      setLoading(true);
      const found = await client.request<SearchResult>(
        "/v1/context/search",
        query,
        request.signal,
      );
      if (!request.current()) return;
      if (found.items.some((hit) => hit.source.project_id !== project.id))
        throw new Error("Сервер вернул источники другого проекта.");
      setResult(found);
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  return (
    <section className="context-view pb-8">
      <div className="page-intro [&_h1]:mb-2 [&_p]:text-muted [&_p]:text-base mobile:[&_h1]:text-2xl mobile:[&_p]:text-sm">
        <h1>{translate("Поиск по истории разработки")}</h1>
        <p>{translate("Сессии, задачи и код ваших проектов.")}</p>
      </div>
      <form
        className="search-form mx-0 mt-7 mb-5"
        onSubmit={(event) => void search(event)}
      >
        <label htmlFor="search">
          {translate("Запрос или полный SHA коммита")}
        </label>
        <div
          className={`
          search-line flex gap-3 [&_input]:[flex:1] [&_input]:min-w-0 [&_input]:px-4 [&_input]:py-3
          [&_input]:text-base [&_button]:min-w-40 mobile:grid
          mobile:[&_button]:w-full
        `}
        >
          <input
            id="search"
            type="search"
            autoComplete="off"
            value={text}
            maxLength={512}
            onChange={(e) => {
              setText(e.target.value);
              clearResults();
            }}
            placeholder={translate("Например, обновление токена доступа")}
          />
          <button
            className={buttonStyles.primary}
            disabled={loading || !text.trim()}
          >
            {loading ? translate("Ищем…") : translate("Найти контекст")}
          </button>
        </div>
        <div
          className={`
          filters flex gap-4 items-end mt-4 flex-wrap [&_label]:text-xs [&_label]:text-muted
          [&_label]:font-normal [&_label]:m-0 [&_select]:block [&_select]:text-sm [&_select]:mt-1
          [&_select]:min-w-45 [&_select]:min-h-11 [&_select]:px-3 [&_select]:py-2 [&_.checkbox]:flex
          [&_.checkbox]:gap-2 [&_.checkbox]:items-center [&_.checkbox]:text-sm [&_.checkbox]:min-h-11
          compact:gap-3 compact:[&_select]:min-w-40
          mobile:grid mobile:grid-cols-[1fr_1fr]
          mobile:gap-3 mobile:[&_select]:min-w-0
          mobile:[&_select]:w-full mobile:[&_select]:text-base
          mobile:[&_.checkbox]:[grid-column:1/-1]
        `}
        >
          <label>
            {translate("Репозиторий")}
            <select
              value={repository}
              onChange={(e) => {
                setRepository(e.target.value);
                clearResults();
              }}
            >
              <option value="">{translate("Все репозитории")}</option>
              {repositories.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            {translate("Источник")}
            <select
              value={kind}
              onChange={(e) => {
                setKind(e.target.value);
                clearResults();
              }}
            >
              <option value="">{translate("Все источники")}</option>
              {Object.entries(sourceNames).map(([value, title]) => (
                <option key={value} value={value}>
                  {translate(title)}
                </option>
              ))}
            </select>
          </label>
          <label className="checkbox [&_input]:[accent-color:var(--color-accent)] [&_input]:w-4 [&_input]:min-h-4 [&_input]:m-0">
            <input
              type="checkbox"
              checked={history}
              onChange={(e) => {
                setHistory(e.target.checked);
                clearResults();
              }}
            />
            {translate("Исторические версии")}
          </label>
        </div>
      </form>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted" role="status">
          {translate("Проверяем доступ и ищем источники…")}
        </p>
      )}
      {result ? (
        <>
          <div
            className={`
              result-summary flex gap-4 justify-between items-baseline text-sm text-muted px-0 py-4 [&_strong]:text-ink
              [&_strong]:font-medium [&_strong]:whitespace-nowrap mobile:items-start
            `}
            role="status"
          >
            <strong>
              {translate("Фрагменты:")} {result.items.length}
            </strong>
            <span>
              {result.pending_sources_in_project > 0 &&
                translate(
                  "Ожидают индексации: {0}. ",
                  result.pending_sources_in_project,
                )}
              {result.failed_sources_in_project > 0 &&
                translate(
                  "Ошибки индексации: {0}. ",
                  result.failed_sources_in_project,
                )}
              {result.has_more &&
                translate("Показана часть результатов. Уточните запрос.")}
            </span>
          </div>
          {result.items.length === 0 ? (
            <Empty title={translate("Совпадений пока нет")}>
              {translate(
                "Уточните формулировку, проверьте фильтры или дождитесь индексации новых источников.",
              )}
            </Empty>
          ) : (
            <div
              className={`
              evidence-layout grid grid-cols-[minmax(0,_1fr)_minmax(340px,_0.95fr)]
              border-t border-line items-start bg-paper
              border-b border-line
              wide:grid-cols-[minmax(0,_1.05fr)_minmax(380px,_1fr)]
              compact:grid-cols-[minmax(0,_1fr)]
            `}
            >
              <div
                className="results min-w-0"
                role="region"
                aria-label={translate("Результаты поиска")}
              >
                {result.items.map((hit) => (
                  <button
                    className={`result-row w-full border-b border-line [border-left:3px_solid_transparent] rounded-none p-5 text-left block bg-paper font-normal text-ink wrap-anywhere [&:last-child]:border-b-0 [&:hover:not(.selected)]:bg-canvas [&.selected]:[border-left-color:var(--color-accent)] [&.selected]:bg-accent-soft mobile:px-4 mobile:py-4 border-t-0 border-r-0 ${selected?.id === hit.id ? "selected" : ""}`}
                    aria-pressed={selected?.id === hit.id}
                    key={hit.id}
                    onClick={() => setSelected(hit)}
                  >
                    <span className="result-meta flex flex-wrap items-center gap-2 justify-between text-xs text-muted mb-3">
                      <span>
                        {translate(sourceNames[hit.kind] ?? hit.kind)}
                      </span>
                      <span
                        className={`freshness bg-canvas rounded px-2 py-1 text-xs [&.current]:bg-success-soft [&.current]:text-success ${hit.current ? "current" : ""}`}
                      >
                        {hit.current
                          ? translate("Последняя запись")
                          : translate("Историческая запись")}
                      </span>
                    </span>
                    <span className="result-title block font-medium text-lg mb-3">
                      {hit.title}
                    </span>
                    <span className="excerpt block text-sm whitespace-pre-wrap leading-[1.6] mb-4 max-h-[135px] overflow-hidden">
                      {hit.content}
                    </span>
                    <span className="result-footer flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted">
                      <span>{date(hit.observed_at)}</span>
                      <span>
                        {hit.confirmed_count > 0
                          ? translate(
                              "Подтверждено коммитов: {0}",
                              hit.confirmed_count,
                            )
                          : hit.source.kind === "capture"
                            ? translate("Запись агента")
                            : hit.source.kind === "code"
                              ? translate("Проверен Git blob")
                              : translate("Наблюдение Tasktopia")}
                      </span>
                    </span>
                  </button>
                ))}
              </div>
              <aside
                className={`
                  inspector min-w-0 border-l border-line min-h-100 sticky top-0 max-h-[100dvh] overflow-auto
                  compact:static compact:max-h-[none]
                  compact:border-l-0 compact:border-t border-line
                `}
                aria-label={translate("Точный источник")}
              >
                {selected ? (
                  <Evidence key={selected.id} client={client} hit={selected} />
                ) : (
                  <Empty title={translate("Откройте фрагмент")}>
                    {translate(
                      "Здесь появятся полный фрагмент, точная версия и происхождение источника.",
                    )}
                  </Empty>
                )}
              </aside>
            </div>
          )}
        </>
      ) : (
        !loading &&
        !error && (
          <p className="search-empty mt-6 border-t border-line pt-6 text-sm text-muted max-w-prose">
            {translate(
              "Откройте результат поиска, чтобы увидеть точную версию источника, связанные коммиты и задачи.",
            )}
          </p>
        )
      )}
    </section>
  );
}
