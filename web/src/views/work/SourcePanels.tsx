import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import type { ReactNode } from "react";
import { safeExternalURL } from "../../domain/context.js";
import type {
  Excerpt,
  MRContext,
  Selection,
  TaskContext,
} from "../../domain/work.js";
import { date } from "../shared.js";
import { observedNumber } from "../../domain/work.js";

function SourceLink({ url, children }: { url: string; children: ReactNode }) {
  const safe = safeExternalURL(url);
  return safe ? (
    <a href={safe} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ) : (
    <span>{children}</span>
  );
}
function Quoted({ value }: { value: Excerpt }) {
  return (
    <>
      {value.text && (
        <pre className="source-prose wrap-anywhere">{value.text}</pre>
      )}
      {value.truncated && (
        <p className="small muted text-muted text-sm">
          {translate("Текст сокращён в контексте.")}
        </p>
      )}
    </>
  );
}
function Count({ shown, total }: { shown: number; total: number }) {
  return shown < total ? (
    <p className="small muted text-muted text-sm">
      {translate("Показано")} {shown} {translate("из")} {total}
      {translate(". Полная запись доступна в источнике.")}
    </p>
  ) : null;
}

export function TaskPanel({
  task: t,
  project,
  onNavigate,
}: {
  task: TaskContext;
  project: string;
  onNavigate: (q: Selection) => void;
}) {
  return (
    <section
      className={`
        work-section task-context border-t border-line px-0 py-6 [&_h3]:mx-0 [&_h3]:mt-6
        [&_h3]:mb-3 [&_h3]:text-lg [&_h3]:font-medium [&_h4]:mx-0 [&_h4]:mt-4 [&_h4]:mb-2
        [&_h4]:text-sm [&_details]:mx-0 [&_details]:my-4 [&_summary]:cursor-pointer [&_summary]:px-0
        [&_summary]:py-2 [&_.source-prose]:[font-family:inherit] [&_.source-prose]:[font-size:inherit]
        [&_.source-prose]:[font-weight:inherit] [&_.source-prose]:leading-[1.65] [&_.source-prose]:whitespace-pre-wrap
        [&_.source-prose]:max-w-[76ch]
      `}
      aria-label={translate("Задача Tasktopia")}
    >
      <h2>
        #{observedNumber(t.number)} {t.title}
      </h2>
      <p>
        {t.status} {translate("· Выполнение по Tasktopia:")} {t.progress}%
      </p>
      <p className="small muted text-muted text-sm">
        {translate("Наблюдение")} {date(t.observed_at)}.{" "}
        <SourceLink url={t.url}>{translate("Открыть в Tasktopia")}</SourceLink>
      </p>
      <dl className={sharedStyles.sourceDetails}>
        <dt>{translate("Страна")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {t.source.country_id}
        </dd>
        <dt>{translate("Задача")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {t.source.task_id}
        </dd>
        <dt>Digest</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {t.source.digest}
        </dd>
      </dl>
      <h3>{translate("Описание")}</h3>
      <Quoted value={t.description} />
      <h3>{translate("Критерии приёмки")}</h3>
      <Quoted value={t.acceptance_criteria} />
      <details>
        <summary>
          {translate("Чек-лист:")} {t.checklist_count}
        </summary>
        <ul>
          {t.checklist.map((c) => (
            <li key={c.id}>
              {c.done ? translate("Выполнено") : translate("Открыто")}:{" "}
              {c.title}
            </li>
          ))}
        </ul>
        <Count shown={t.checklist.length} total={t.checklist_count} />
      </details>
      <details>
        <summary>
          {translate("Комментарии:")} {t.comment_count}
        </summary>
        {t.comments.map((c) => (
          <article key={c.id}>
            <p className="small muted text-muted text-sm">
              {date(c.created_at)}
            </p>
            <Quoted value={c.body} />
          </article>
        ))}
        <Count shown={t.comments.length} total={t.comment_count} />
      </details>
      <details>
        <summary>
          {translate("Дефекты:")} {t.defect_count}
          {translate(", открытых:")} {t.open_defect_count}
        </summary>
        {t.defects.map((d) => (
          <article key={d.id}>
            <h4>
              {d.title} ({d.status})
            </h4>
            <Quoted value={d.details} />
          </article>
        ))}
        <Count shown={t.defects.length} total={t.defect_count} />
      </details>
      <details>
        <summary>
          {translate("Зависимости:")} {t.dependency_count}
        </summary>
        <ul>
          {t.dependencies.map((d) => (
            <li key={d.id}>
              <button
                className={buttonStyles.text}
                onClick={() =>
                  onNavigate({
                    project_id: project,
                    country_id: t.source.country_id!,
                    task_id: d.id,
                  })
                }
              >
                #{observedNumber(d.taskNumber)} {d.title}
              </button>
              <span>{d.status}</span>
            </li>
          ))}
        </ul>
        <Count shown={t.dependencies.length} total={t.dependency_count} />
      </details>
      <details>
        <summary>
          {translate("Документы:")} {t.document_count}
        </summary>
        <ul>
          {t.documents.map((d) => (
            <li key={d.id}>
              {d.title}{" "}
              <span className="mono small font-mono text-xs wrap-anywhere">
                {d.file_name}
              </span>
            </li>
          ))}
        </ul>
        <p className="small muted text-muted text-sm">
          {translate(
            "Текст документов доступен через точные цитаты и поиск контекста.",
          )}
        </p>
        <Count shown={t.documents.length} total={t.document_count} />
      </details>
      {t.links.length > 0 && (
        <ul>
          {t.links.map((l, i) => (
            <li key={i}>
              <SourceLink url={l.url}>{l.title || l.url}</SourceLink>
            </li>
          ))}
        </ul>
      )}
      <Count shown={t.links.length} total={t.link_count} />
      {t.children_clipped && (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate("Часть полей дочерних записей сокращена.")}
        </p>
      )}
    </section>
  );
}

const bases: Record<string, string> = {
  explicit_provider_mr: "Явно выбранный MR/PR",
  captured_stream_commits: "Подтверждённые коммиты связанного потока сессии",
  capture_task_binding_and_confirmed_commit:
    "Привязка агента к задаче и подтверждённое членство коммита",
  confirmed_commit_membership: "Подтверждённое членство указанного коммита",
};

export function MRPanel({
  mr: m,
  project,
  onNavigate,
}: {
  mr: MRContext;
  project: string;
  onNavigate: (q: Selection) => void;
}) {
  function open() {
    if (!Number.isSafeInteger(m.iid)) return;
    onNavigate({
      project_id: project,
      repository_id: m.repository_id,
      merge_request_iid: m.iid,
      ...(m.observed_head_sha
        ? { expected_head_sha: m.observed_head_sha }
        : {}),
    });
  }
  return (
    <article
      className="related-source mr-context wrap-anywhere px-0 py-4 border-b border-line"
      aria-label={`MR ${observedNumber(m.iid)}`}
    >
      <h3>
        !{observedNumber(m.iid)} {m.title}
      </h3>
      <p>
        {m.state} ·{" "}
        <SourceLink url={m.url}>{translate("Открыть у провайдера")}</SourceLink>
      </p>
      <p className="small text-sm">
        {translate("Основание связи:")}{" "}
        {translate(bases[m.association_basis] ?? "Наблюдение провайдера")}.
      </p>
      <p className="small muted text-muted text-sm">
        {translate("Наблюдение")} {date(m.observed_at)}
        {translate(". Обновлено у провайдера")} {date(m.provider_updated_at)}.
      </p>
      {m.expected_head_matches === false && (
        <p
          className="notice head-mismatch px-4 py-4 mx-0 my-3 text-muted"
          role="alert"
        >
          {translate(
            "Head MR изменился относительно выбранного SHA. Приведённые CI относятся к наблюдаемому head, а не к ожидавшемуся.",
          )}
        </p>
      )}
      <dl className={sharedStyles.sourceDetails}>
        <dt>{translate("Наблюдаемый head")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {m.observed_head_sha || translate("Нет наблюдаемого SHA")}
        </dd>
        <dt>{translate("ID репозитория источника")}</dt>
        <dd>{observedNumber(m.source_project_id)}</dd>
        <dt>{translate("Наблюдение")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {m.observation}
        </dd>
      </dl>
      <Quoted value={m.description} />
      <button
        className={buttonStyles.secondary}
        disabled={!Number.isSafeInteger(m.iid)}
        onClick={open}
      >
        {m.expected_head_matches === false
          ? translate("Открыть наблюдаемый head")
          : translate("Открыть контекст MR")}
      </button>
      <h4>{translate("CI для наблюдаемого head")}</h4>
      {!m.pipelines_for_observed_head.length && (
        <p className="muted small text-muted text-sm">
          {translate(
            "Подходящих наблюдений CI нет. Успешная проверка не предполагается.",
          )}
        </p>
      )}
      {m.pipelines_for_observed_head.map((p) => (
        <div
          className="ci-observation [border-left:3px_solid_var(--color-control)] pl-4 mx-0 my-4 [&_p]:mb-1"
          key={`${p.project_id}/${p.id}`}
        >
          <p>
            <SourceLink url={p.url ?? ""}>
              Pipeline {observedNumber(p.id)}
            </SourceLink>
            : <strong>{p.status}</strong>
          </p>
          <p className="mono small font-mono text-xs wrap-anywhere">{p.sha}</p>
          <p className="small text-sm">
            {translate("ID репозитория источника")}{" "}
            {observedNumber(p.project_id)}, {p.ref}, {p.source ?? ""}.{" "}
            {p.matches_observed_head
              ? translate("Совпадает с наблюдаемым head.")
              : translate("Не применяется к наблюдаемому head.")}{" "}
            {date(p.updated_at)}
          </p>
        </div>
      ))}
      {m.pipelines_have_more && (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate("Показана часть CI. Полный список доступен у провайдера.")}
        </p>
      )}
      <h4>{translate("Наблюдения deployment")}</h4>
      {m.deployments_for_result_or_head.map((d) => (
        <p className="small deployment-observation text-sm" key={d.id}>
          {d.environment}: {d.status}
          <br />
          <span className="mono font-mono text-xs wrap-anywhere">{d.sha}</span>
          <br />
          {date(d.updated_at)}
        </p>
      ))}
      {!m.deployments_for_result_or_head.length && (
        <p className="muted small text-muted text-sm">
          {translate("Подходящих наблюдений deployment нет.")}
        </p>
      )}
      {m.deployments_have_more && (
        <p className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate("Показана часть deployment.")}
        </p>
      )}
      <p className="small text-sm">
        {translate("Членство исходных коммитов:")}{" "}
        {m.source_commits_complete
          ? translate("сверено для этого code key")
          : translate("полная сверка не завершена")}
      </p>
      {m.result_shas.length > 0 && (
        <div>
          <h4>{translate("Подтверждённые результирующие SHA")}</h4>
          <ul className="source-links wrap-anywhere pl-5 [&_li_>_span]:block">
            {m.result_shas.map((sha) => (
              <li key={sha}>
                <button
                  className={`
                    text-button mono px-0 py-1 min-h-8 bg-transparent border-0 text-accent justify-start font-mono
                    text-xs wrap-anywhere [&.mono]:whitespace-normal [&.mono]:text-left
                    [&.mono]:wrap-anywhere [&.mono]:max-w-full
                  `}
                  onClick={() =>
                    onNavigate({
                      project_id: project,
                      repository_id: m.repository_id,
                      commit_sha: sha,
                    })
                  }
                >
                  {sha}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </article>
  );
}
