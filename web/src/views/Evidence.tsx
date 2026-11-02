import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useEffect, useState } from "react";
import { LatestRequest } from "../application/latest.js";
import { readableError, sourceNames } from "../domain/context.js";
import type { Expanded, Hit } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { ErrorNotice, date, aborted } from "./shared.js";
import { SourceMetadata } from "./SourceMetadata.js";
import { CodeProvenance } from "./CodeProvenance.js";

export function Evidence({
  client,
  hit,
  onOpen,
}: {
  client: ApiClient;
  hit: Hit;
  onOpen: (hit: Hit) => void;
}) {
  const [expanded, setExpanded] = useState<Expanded | null>(null);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(next = 0) {
    const request = requests.begin();
    setLoading(true);
    setOffset(next);
    setError("");
    if (!next) setExpanded(null);
    try {
      const value = await client.request<Expanded>(
        "/v1/context/expand",
        { evidence_id: hit.id, offset: next, limit: 1200 },
        request.signal,
      );
      if (!request.current()) return;
      if (
        value.id !== hit.id ||
        value.source.project_id !== hit.source.project_id ||
        value.source.digest !== hit.source.digest ||
        value.content_digest !== hit.content_digest
      )
        throw new Error("Ответ не совпал с выбранным источником.");
      setExpanded((previous) =>
        next && previous
          ? {
              ...value,
              start: previous.start,
              content: previous.content + value.content,
            }
          : value,
      );
      setOffset(next);
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [hit.id, client, requests]);
  const source = hit.source;
  const current = expanded ?? hit;
  return (
    <div
      className={`
      evidence-detail p-5 [&_h2]:text-xl [&_h2]:mx-0 [&_h2]:my-2 [&_header_p]:text-xs
      mobile:px-4 mobile:py-5
    `}
    >
      <header>
        <span className="source-kind text-sm text-accent">
          {translate(sourceNames[hit.kind] ?? hit.kind)}
        </span>
        <h2>{hit.title}</h2>
        <SourceMetadata metadata={current.metadata} />
        <p className="muted text-muted">
          {source.commit_sha
            ? translate("Коммит {0}", source.commit_sha.slice(0, 10))
            : current.current
              ? translate("Последняя сохранённая версия")
              : translate("Историческая версия")}
        </p>
      </header>
      <details className="mt-4 text-sm text-muted">
        <summary className="py-2">{translate("Сведения об источнике")}</summary>
        <dl className={sharedStyles.sourceDetails}>
          {current.metadata?.provider && (
            <>
              <dt>{translate("Провайдер")}</dt>
              <dd>{current.metadata.provider}</dd>
            </>
          )}
          {current.metadata?.version &&
            current.metadata.version !== "unknown" && (
              <>
                <dt>{translate("Версия интеграции")}</dt>
                <dd>{current.metadata.version}</dd>
              </>
            )}
          {current.metadata?.profile && (
            <>
              <dt>{translate("Профиль агента")}</dt>
              <dd>{current.metadata.profile}</dd>
            </>
          )}
          {current.metadata?.model && (
            <>
              <dt>
                {current.metadata.model_basis === "message_metadata"
                  ? translate("Модель сообщения")
                  : current.metadata.model_basis === "import_report"
                    ? translate("Модель из отчёта")
                    : translate("Настройка модели сессии")}
              </dt>
              <dd>{current.metadata.model}</dd>
            </>
          )}
          {source.session_id && (
            <>
              <dt>{translate("Сессия")}</dt>
              <dd className="mono font-mono text-xs wrap-anywhere">
                {source.session_id}
              </dd>
              <dt>{translate("Ревизия")}</dt>
              <dd>{source.revision}</dd>
            </>
          )}
          {source.file_path && (
            <>
              <dt>{translate("Файл")}</dt>
              <dd className="mono font-mono text-xs wrap-anywhere">
                {source.file_path}
              </dd>
            </>
          )}
          {source.commit_sha && (
            <>
              <dt>{translate("Коммит репозитория")}</dt>
              <dd className="mono font-mono text-xs wrap-anywhere">
                {source.commit_sha}
              </dd>
            </>
          )}
          {source.repository_id && (
            <>
              <dt>{translate("Репозиторий")}</dt>
              <dd className="mono font-mono text-xs wrap-anywhere">
                {source.repository_id}
              </dd>
            </>
          )}
          <dt>{translate("Сохранено")}</dt>
          <dd>{date(hit.observed_at)}</dd>
        </dl>
      </details>
      {hit.kind === "code" &&
        source.repository_id &&
        source.commit_sha &&
        source.file_path && (
          <CodeProvenance client={client} hit={hit} onOpen={onOpen} />
        )}
      {current.confirmed_count > 0 && (
        <details
          className={`
          confirmed text-xs border-t border-line pt-4 mt-6 [&_summary]:cursor-pointer
          [&_summary]:min-h-[30px] [&_summary]:text-muted [&_ul]:pl-4
        `}
        >
          <summary>
            {translate("Подтверждено провайдером:")} {current.confirmed_count}
          </summary>
          <ul>
            {current.confirmed_commits.map((sha) => (
              <li className="mono font-mono text-xs wrap-anywhere" key={sha}>
                {sha}
              </li>
            ))}
          </ul>
        </details>
      )}
      <ErrorNotice>{error}</ErrorNotice>
      {error && (
        <button
          className={buttonStyles.secondary}
          onClick={() => void load(offset)}
        >
          {translate("Повторить чтение")}
        </button>
      )}
      {loading && (
        <p role="status" className="notice px-4 py-4 mx-0 my-3 text-muted">
          {translate("Загружаем фрагмент…")}
        </p>
      )}
      {expanded && (
        <>
          <div className="quote-meta flex gap-3 justify-between items-baseline text-xs px-0 pt-4 pb-3 [&_strong]:font-medium [&_strong]:text-sm">
            <strong>{translate("Фрагмент источника")}</strong>
            <span>
              {expanded.line_start
                ? translate(
                    "Строки {0}–{1}",
                    expanded.line_start,
                    expanded.line_end,
                  )
                : translate("Символы {0}–{1}", expanded.start, expanded.end)}
            </span>
          </div>
          <pre
            className={`source-quote [font-family:inherit] text-sm leading-[1.65] whitespace-pre-wrap wrap-anywhere bg-canvas border border-line rounded-[6px] p-4 m-0 [&.code]:font-mono [&.code]:text-xs ${hit.kind === "code" ? "code" : ""}`}
          >
            {expanded.content}
          </pre>
          <div className="quote-pagination flex gap-2 items-center flex-wrap mt-4 [&_button]:text-xs [&_button]:px-3 [&_button]:py-2 [&_button]:min-h-9">
            {expanded.next_offset ? (
              <button
                className={buttonStyles.secondary}
                disabled={loading}
                onClick={() => void load(expanded.next_offset)}
              >
                {translate("Загрузить ещё")}
              </button>
            ) : (
              <span className="muted small text-muted text-sm">
                {translate("Фрагмент прочитан до конца")}
              </span>
            )}
          </div>
        </>
      )}
      <details
        className={`
        digests text-xs border-t border-line pt-4 mt-6 [&_summary]:cursor-pointer
        [&_summary]:min-h-[30px] [&_summary]:text-muted [&_dt]:mx-0 [&_dt]:mt-3 [&_dt]:mb-1 [&_dt]:text-muted
        [&_dd]:text-xs
      `}
      >
        <summary>{translate("Идентификаторы источника")}</summary>
        <dl>
          <dt>{translate("Источник SHA-256")}</dt>
          <dd className="mono font-mono text-xs wrap-anywhere">
            {source.digest}
          </dd>
          <dt>{translate("Фрагмент SHA-256")}</dt>
          <dd className="mono font-mono text-xs wrap-anywhere">
            {hit.content_digest}
          </dd>
          <dt>Evidence ID</dt>
          <dd className="mono font-mono text-xs wrap-anywhere">{hit.id}</dd>
        </dl>
      </details>
    </div>
  );
}
