import { t } from "../../domain/locale.js";
import type { MRContext, Selection } from "../../domain/history.js";
import { observedNumber } from "../../domain/history.js";
import { safeExternalURL } from "../../domain/context.js";
import { buttonStyles } from "../ui.js";

export function MRPanel({
  mr,
  project,
  onNavigate,
}: {
  mr: MRContext;
  project: string;
  onNavigate: (q: Selection) => void;
}) {
  const url = safeExternalURL(mr.url);
  const github = mr.url.includes("/pull/");
  const stateLabel: Record<string, string> = {
    opened: "Открыт",
    open: "Открыт",
    merged: "Слит",
    closed: "Закрыт",
  };
  const pipelineLabel: Record<string, string> = {
    success: "Успешно",
    failed: "Ошибка",
    running: "Выполняется",
    pending: "Ожидает",
    skipped: "Пропущено",
    canceled: "Отменено",
  };
  const commits = mr.source_commits ?? [];
  function commit(sha: string) {
    if (mr.source_repository_id)
      onNavigate({
        project_id: project,
        repository_id: mr.source_repository_id,
        commit_sha: sha,
      });
  }
  return (
    <article className="mr-context min-w-0 rounded-lg border border-line bg-paper p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <span className="text-xs text-muted">
            {github ? "GitHub PR" : "GitLab MR"} {github ? "#" : "!"}
            {observedNumber(mr.iid)}
          </span>
          <h2 className="mt-1 mb-2 text-xl">{mr.title}</h2>
          <span className="rounded bg-canvas px-2 py-1 text-xs text-muted">
            {t(stateLabel[mr.state] ?? mr.state)}
          </span>
        </div>
        {url && (
          <a
            className={buttonStyles.secondary + " action-link"}
            href={url}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t("Открыть в {0}", github ? "GitHub" : "GitLab")}
          </a>
        )}
      </div>
      {mr.expected_head_matches === false && (
        <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-l-2 border-accent bg-accent-soft p-3 text-sm">
          <span>{t("В MR/PR появились новые коммиты.")}</span>
          <button
            className={buttonStyles.text}
            onClick={() =>
              onNavigate({
                project_id: project,
                repository_id: mr.repository_id,
                merge_request_iid: mr.iid,
                expected_head_sha: mr.observed_head_sha,
              })
            }
          >
            {t("Открыть последнюю версию")}
          </button>
        </div>
      )}
      {mr.description.text && (
        <p className="mt-4 mb-0 max-w-[76ch] whitespace-pre-wrap wrap-anywhere text-sm text-muted">
          {mr.description.text}
        </p>
      )}
      <div className="mt-5 border-t border-line pt-4">
        <h3 className="mt-0 mb-3 text-sm font-medium">
          {t("Коммиты")}{" "}
          <span className="font-normal text-muted">
            {mr.source_commit_count ?? commits.length}
          </span>
        </h3>
        {!mr.source_commits_complete && (
          <p className="text-sm text-muted">
            {t("Список коммитов обновляется.")}
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          {commits.slice(0, 8).map((sha) => (
            <button
              key={sha}
              disabled={!mr.source_repository_id}
              className={
                buttonStyles.secondary + " min-h-9 px-3 font-mono text-xs"
              }
              title={sha}
              onClick={() => commit(sha)}
            >
              {sha.slice(0, 10)}
            </button>
          ))}
        </div>
        {commits.length > 8 && (
          <details className="mt-3 text-sm">
            <summary className="py-2">{t("Все доступные коммиты")}</summary>
            <div className="mt-2 flex flex-wrap gap-2">
              {commits.slice(8).map((sha) => (
                <button
                  key={sha}
                  className={
                    buttonStyles.secondary + " min-h-9 px-3 font-mono text-xs"
                  }
                  disabled={!mr.source_repository_id}
                  title={sha}
                  onClick={() => commit(sha)}
                >
                  {sha.slice(0, 10)}
                </button>
              ))}
            </div>
          </details>
        )}
        {mr.source_commits_more && (
          <p className="mt-3 mb-0 text-xs text-muted">
            {t("Показаны первые 50 коммитов.")}
          </p>
        )}
        {!mr.source_repository_id && (
          <p className="mt-3 mb-0 text-sm text-muted">
            {t(
              "Подключите исходный репозиторий, чтобы открыть связанные сессии.",
            )}
          </p>
        )}
      </div>
      {mr.pipelines_for_observed_head.length > 0 && (
        <details className="mt-4 border-t border-line pt-2 text-sm">
          <summary className="py-2">{t("Проверки CI")}</summary>
          <ul className="m-0 list-none p-0">
            {mr.pipelines_for_observed_head.map((p) => (
              <li
                key={p.id}
                className="flex flex-wrap justify-between gap-3 border-t border-line py-3"
              >
                <span>{p.ref}</span>
                <span
                  className={
                    p.status === "success" ? "text-success" : "text-muted"
                  }
                >
                  {t(pipelineLabel[p.status] ?? p.status)}
                </span>
                <code className="text-xs text-muted">{p.sha.slice(0, 10)}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
    </article>
  );
}
