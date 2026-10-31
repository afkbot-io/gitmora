import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { Hit } from "../../domain/context.js";
import { readableError, sourceNames } from "../../domain/context.js";
import type {
  CaptureCursor,
  ContextPack,
  MRCursor,
  Selection,
} from "../../domain/work.js";
import { packQuery, sameSelection } from "../../domain/work.js";
import type { ApiClient } from "../../transport/api.js";
import { LatestRequest } from "../../application/latest.js";
import { Empty, ErrorNotice, aborted, date } from "../shared.js";
import { Evidence } from "../Evidence.js";
import { TaskPanel, MRPanel } from "./SourcePanels.js";
import { SessionTranscript } from "./SessionTranscript.js";

interface Pages {
  capture_after?: CaptureCursor;
  merge_requests_after?: MRCursor;
}

/** The backend pack owns association/availability rules; the view retains source identities and explicit omissions. */
export function WorkDetail({
  client,
  selection,
  onNavigate,
}: {
  client: ApiClient;
  selection: Selection;
  onNavigate: (q: Selection) => void;
}) {
  const [pack, setPack] = useState<ContextPack | null>(null);
  const [pages, setPages] = useState<Pages>({});
  const [hit, setHit] = useState<Hit | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(next: Pages = pages) {
    const request = requests.begin();
    setPack(null);
    setHit(null);
    setLoading(true);
    setError("");
    try {
      const query = packQuery({ ...selection, ...next });
      setPages(next);
      const value = await client.request<ContextPack>(
        "/v1/context/pack",
        query,
        request.signal,
      );
      if (!request.current()) return;
      if (
        value.schema_version !== 1 ||
        !sameSelection(selection, value.query) ||
        value.evidence.some(
          (e) => e.source.project_id !== selection.project_id,
        ) ||
        value.captures.items.some(
          (c) => c.source.project_id !== selection.project_id,
        ) ||
        (value.task && value.task.source.project_id !== selection.project_id)
      )
        throw new Error("Контекст не совпал с выбранным источником.");
      setPack(value);
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load({});
    return () => requests.cancel();
  }, [client, selection, requests]);
  const mergeRequests = pack && (
    <section
      className={sharedStyles.workSection}
      aria-label={translate("Связанные MR")}
    >
      <h2>Merge requests</h2>
      {pack.merge_requests.omitted_for_permissions ? (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate(
            "Связи с MR скрыты: нужны разрешения на соответствующие источники.",
          )}
        </p>
      ) : (
        <>
          {!pack.merge_requests.items.length && (
            <p className="muted text-muted">
              {translate(
                "Доступных подтверждённых связей с MR пока нет. Это не доказывает отсутствие MR в GitLab.",
              )}
            </p>
          )}
          {pack.merge_requests.items.map((m) => (
            <MRPanel
              key={`${m.repository_id}/${m.iid}`}
              mr={m}
              project={selection.project_id}
              onNavigate={onNavigate}
            />
          ))}
          {(pages.merge_requests_after || pack.merge_requests.next) && (
            <div className="transcript-pages flex flex-wrap items-end gap-3 mx-0 my-4">
              {pages.merge_requests_after && (
                <button
                  className={buttonStyles.secondary}
                  onClick={() => {
                    const { merge_requests_after: _, ...rest } = pages;
                    void load(rest);
                  }}
                >
                  {translate("Первые MR")}
                </button>
              )}
              {pack.merge_requests.next && (
                <button
                  className={buttonStyles.secondary}
                  onClick={() =>
                    void load({
                      ...pages,
                      merge_requests_after: pack.merge_requests.next!,
                    })
                  }
                >
                  {translate("Следующие MR")}
                </button>
              )}
            </div>
          )}
          {pack.merge_requests.has_more && !pack.merge_requests.next && (
            <p className="notice px-4 py-4 mx-0 my-3 text-muted">
              {translate("Есть дополнительные MR, не вошедшие в контекст.")}
            </p>
          )}
        </>
      )}
    </section>
  );
  return (
    <section className="work-detail min-w-0 [&_>_.section-heading]:mb-4 [&_>_.section-heading]:flex-wrap">
      <div className={sharedStyles.heading}>
        <h2>{translate("Контекст источника")}</h2>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {translate("Обновить контекст")}
        </button>
      </div>
      <p className="mono small work-scope font-mono text-xs wrap-anywhere">
        {selection.task_id ??
          selection.session_id ??
          selection.commit_sha ??
          `MR !${selection.merge_request_iid}`}
      </p>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted" role="status">
          {translate("Собираем доступные наблюдения…")}
        </p>
      )}
      {pack && (
        <>
          <p className="small muted text-muted text-sm">
            {translate("Прочитано")} {date(pack.read_at)}
            {translate(
              ". Данные отражают последние доступные наблюдения источников и не являются единым снимком GitLab и Tasktopia.",
            )}
          </p>
          {(pack.pending_sources_in_project > 0 ||
            pack.failed_sources_in_project > 0) && (
            <p className="notice px-4 py-4 mx-0 my-3 text-muted">
              {translate("Ожидают индексации:")}{" "}
              {pack.pending_sources_in_project}
              {translate(". Ошибки индексации:")}{" "}
              {pack.failed_sources_in_project}
              {translate(". Цитаты могут появиться позже.")}
            </p>
          )}
          {pack.budget_limited && (
            <p className="notice px-4 py-4 mx-0 my-3 text-muted">
              {translate(
                "Часть деталей сокращена до лимита контекста. Используйте точные источники и продолжение списков.",
              )}
            </p>
          )}
          {pack.task ? (
            <TaskPanel
              task={pack.task}
              project={selection.project_id}
              onNavigate={onNavigate}
            />
          ) : (
            selection.task_id && (
              <Empty
                title={
                  pack.task_state === "not_permitted"
                    ? translate("Нет разрешения на данные задачи")
                    : translate("Задача пока недоступна")
                }
              >
                {translate(
                  "Привязка агента не подтверждает доступность задачи в Tasktopia. Проверьте синхронизацию и права.",
                )}
              </Empty>
            )
          )}
          {selection.session_id && (
            <SessionTranscript
              client={client}
              selection={selection}
              onNavigate={onNavigate}
            />
          )}
          {selection.merge_request_iid && mergeRequests}
          <section
            className={sharedStyles.workSection}
            aria-label={translate("Связанные сессии")}
          >
            <h2>{translate("Окна сессии")}</h2>
            {pack.captures.omitted_for_permissions ? (
              <p className="notice px-4 py-4 mx-0 my-3 text-muted">
                {translate("Сессии скрыты: требуется capture:read.")}
              </p>
            ) : (
              <>
                {!pack.captures.items.length && (
                  <p className="muted text-muted">
                    {translate("Подходящих принятых окон пока нет.")}
                  </p>
                )}
                {pack.captures.items.map((c) => (
                  <article
                    className="related-source wrap-anywhere px-0 py-4 border-b border-line"
                    key={`${c.source.repository_id}/${c.source.session_id}`}
                  >
                    <button
                      className={buttonStyles.text}
                      onClick={() =>
                        onNavigate({
                          project_id: selection.project_id,
                          repository_id: c.source.repository_id!,
                          session_id: c.source.session_id!,
                        })
                      }
                    >
                      {translate("Открыть окно")} {c.segment}: {c.agent}
                    </button>
                    <p className="mono small font-mono text-xs wrap-anywhere">
                      {c.source.session_id}
                    </p>
                    <p className="small text-sm">
                      {translate("Ревизия")} {c.source.revision}
                      {translate(
                        ". Подтверждённых коммитов этой ревизии:",
                      )}{" "}
                      {c.confirmed_count}.
                    </p>
                    <p className="small text-sm">
                      {c.chain_verified
                        ? translate(
                            "Цепочка точных родительских ссылок проверена.",
                          )
                        : translate(
                            "Полная цепочка родительских ссылок не подтверждена.",
                          )}
                      {c.chain_verification_limited
                        ? translate(" Проверка ограничена глубиной.")
                        : ""}
                    </p>
                    {c.continuation && (
                      <p className="small text-sm">
                        {translate("Ссылка на предыдущую ревизию:")}{" "}
                        {c.parent_reference_verified
                          ? translate("проверена")
                          : translate("не подтверждена")}
                        {translate(". Корень, указанный источником:")}{" "}
                        <span className="mono font-mono text-xs wrap-anywhere">
                          {c.root_session_id}
                        </span>
                        .
                      </p>
                    )}
                    <ul className="source-links wrap-anywhere pl-5 [&_li_>_span]:block">
                      {c.confirmed_commits.map((sha) => (
                        <li key={sha}>
                          <button
                            className={`
                              text-button mono px-0 py-1 min-h-8 bg-transparent border-0 text-accent justify-start font-mono
                              text-xs wrap-anywhere [&.mono]:whitespace-normal [&.mono]:text-left
                              [&.mono]:wrap-anywhere [&.mono]:max-w-full
                            `}
                            onClick={() =>
                              onNavigate({
                                project_id: selection.project_id,
                                repository_id: c.source.repository_id!,
                                commit_sha: sha,
                              })
                            }
                          >
                            {sha}
                          </button>
                        </li>
                      ))}
                    </ul>
                    {c.confirmed_count > c.confirmed_commits.length && (
                      <p className="small muted text-muted text-sm">
                        {translate(
                          "Показана часть подтверждённых SHA. Полная запись доступна в деталях сессии.",
                        )}
                      </p>
                    )}
                  </article>
                ))}
                {(pages.capture_after || pack.captures.next) && (
                  <div className="transcript-pages flex flex-wrap items-end gap-3 mx-0 my-4">
                    {pages.capture_after && (
                      <button
                        className={buttonStyles.secondary}
                        onClick={() => {
                          const { capture_after: _, ...rest } = pages;
                          void load(rest);
                        }}
                      >
                        {translate("Первые окна")}
                      </button>
                    )}
                    {pack.captures.next && (
                      <button
                        className={buttonStyles.secondary}
                        onClick={() =>
                          void load({
                            ...pages,
                            capture_after: pack.captures.next!,
                          })
                        }
                      >
                        {translate("Следующие окна")}
                      </button>
                    )}
                  </div>
                )}
              </>
            )}
          </section>
          {!selection.merge_request_iid && mergeRequests}
          <section
            className={sharedStyles.workSection}
            aria-label={translate("Цитаты контекста")}
          >
            <h2>{translate("Точные источники")}</h2>
            {pack.evidence.map((e) => (
              <button
                className={`
                  work-evidence flex flex-col items-stretch text-left text-ink bg-paper border border-line
                  w-full mx-0 my-3 font-normal wrap-anywhere [&[aria-pressed=true]]:[border-color:var(--color-accent)]
                `}
                key={e.id}
                onClick={() => setHit(e)}
                aria-pressed={hit?.id === e.id}
              >
                <span className="small muted text-muted text-sm">
                  {translate(sourceNames[e.kind] ?? e.kind)}
                </span>
                <strong>{e.title}</strong>
                <span>{e.content}</span>
                <span className="small text-sm">
                  {e.current
                    ? translate("Последняя запись")
                    : translate("Историческая запись")}{" "}
                  · {date(e.observed_at)}
                </span>
              </button>
            ))}
            {!pack.evidence.length && (
              <p className="muted text-muted">
                {translate(
                  "Доступных цитат пока нет. Проверьте права и индексацию.",
                )}
              </p>
            )}
            {pack.evidence_has_more && (
              <p className="notice px-4 py-4 mx-0 my-3 text-muted">
                {translate(
                  "Показана часть цитат. Уточните запрос в разделе «Контекст» или откройте отдельный источник.",
                )}
              </p>
            )}
            {hit && (
              <aside
                className="work-quote border border-line bg-paper p-4 [&_>_button]:mb-4"
                aria-label={translate("Фрагмент источника")}
              >
                <button
                  className={buttonStyles.secondary}
                  onClick={() => setHit(null)}
                >
                  {translate("Закрыть цитату")}
                </button>
                <Evidence key={hit.id} client={client} hit={hit} />
              </aside>
            )}
          </section>
        </>
      )}
    </section>
  );
}
