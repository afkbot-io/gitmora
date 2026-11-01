import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Selection, SessionRevision } from "../../domain/history.js";
import type { ApiClient } from "../../transport/api.js";
import { readableError } from "../../domain/context.js";
import { LatestRequest } from "../../application/latest.js";
import { ErrorNotice, aborted, date } from "../shared.js";
import { ImportOriginView } from "./ImportOrigin.js";

/** Read one exact sanitized revision. Tool source text never becomes executable HTML or proof of successful execution. */
export function SessionTranscript({
  client,
  selection,
  onNavigate,
}: {
  client: ApiClient;
  selection: Selection;
  onNavigate: (q: Selection) => void;
}) {
  const [value, setValue] = useState<SessionRevision | null>(null);
  const [revision, setRevision] = useState("");
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(number = 0) {
    const request = requests.begin();
    setValue(null);
    setOffset(0);
    setLoading(true);
    setError("");
    try {
      const result = await client.request<SessionRevision>(
        `/v1/repositories/${selection.repository_id}/sessions/${selection.session_id}`,
        undefined,
        request.signal,
        number ? { revision: number } : undefined,
      );
      if (!request.current()) return;
      if (
        result.snapshot.repository_id !== selection.repository_id ||
        result.snapshot.session_id !== selection.session_id ||
        (number && result.snapshot.revision !== number)
      )
        throw new Error("Ответ не совпал с выбранной сессией и ревизией.");
      setValue(result);
      setRevision(String(result.snapshot.revision));
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, selection.repository_id, selection.session_id, requests]);
  function select(event: FormEvent) {
    event.preventDefault();
    if (!/^[1-9][0-9]*$/.test(revision) || Number(revision) > 1_000_000_000) {
      setError("Введите номер ревизии от 1 до 1000000000.");
      return;
    }
    void load(Number(revision));
  }
  const s = value?.snapshot;
  return (
    <section
      className={`
        work-section transcript [&_li]:wrap-anywhere border-t border-line px-0 py-4
        [&_h3]:mx-0 [&_h3]:mt-6 [&_h3]:mb-3 [&_h3]:text-lg [&_h3]:font-medium [&_h4]:mx-0
        [&_h4]:mt-4 [&_h4]:mb-2 [&_h4]:text-sm [&_details]:mx-0 [&_details]:my-4
        [&_summary]:cursor-pointer [&_summary]:px-0 [&_summary]:py-2 [&_.source-prose]:[font-family:inherit]
        [&_.source-prose]:[font-size:inherit] [&_.source-prose]:[font-weight:inherit] [&_.source-prose]:leading-[1.65]
        [&_.source-prose]:whitespace-pre-wrap [&_.source-prose]:max-w-[76ch]
      `}
      aria-label={translate("Запись сессии")}
    >
      <h2>{translate("Запись сессии агента")}</h2>

      <form
        className="revision-form flex flex-wrap items-end gap-3 mx-0 my-4 [&_label]:max-w-45 [&_label]:m-0 [&_input]:w-full"
        onSubmit={select}
      >
        <label>
          {translate("Ревизия сессии")}
          <input
            type="number"
            min="1"
            max="1000000000"
            step="1"
            required
            value={revision}
            onChange={(e) => setRevision(e.target.value)}
          />
        </label>
        <button className={buttonStyles.secondary} disabled={loading}>
          {translate("Открыть версию")}
        </button>
        <button
          type="button"
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {translate("Последняя версия")}
        </button>
      </form>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && <p role="status">{translate("Читаем запись сессии…")}</p>}
      {s && value && (
        <>
          <dl className={sharedStyles.sourceDetails}>
            <dt>{translate("Сессия")}</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {s.session_id}
            </dd>
            <dt>{translate("Ревизия")}</dt>
            <dd>{s.revision}</dd>
            <dt>{translate("Агент / модель")}</dt>
            <dd>
              {s.agent.name} / {s.agent.model}
            </dd>
            <dt>
              {s.origin?.time_kind === "checkpoint_created"
                ? "Checkpoint"
                : s.origin?.time_kind === "commit_recorded"
                  ? translate("Время коммита")
                  : translate("Начало")}
            </dt>
            <dd>{date(s.started_at)}</dd>
            {s.ended_at && (
              <>
                <dt>{translate("Окончание")}</dt>
                <dd>{date(s.ended_at)}</dd>
              </>
            )}
            <dt>Digest</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {value.digest}
            </dd>
          </dl>

          <details>
            <summary>
              {translate("Коммиты, указанные агентом:")} {s.commits.length}
            </summary>
            <ul className="source-links wrap-anywhere pl-5 [&_li_>_span]:block">
              {s.commits.map((sha) => (
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
                        repository_id: s.repository_id,
                        commit_sha: sha,
                      })
                    }
                  >
                    {sha}
                  </button>
                  <span className="small text-sm">
                    {value.confirmed_commits.includes(sha)
                      ? translate("Подтверждён провайдером")
                      : translate("Коммит ещё не проверен")}
                  </span>
                </li>
              ))}
            </ul>
          </details>
          <details>
            <summary>
              {translate("Пути, указанные агентом:")} {s.files.length}
            </summary>
            <p className="small muted text-muted text-sm">
              {translate(
                "Это пути из записи агента; наличие файла в репозитории проверяется отдельно.",
              )}
            </p>
            <ul>
              {s.files.map((file) => (
                <li className="mono font-mono text-xs wrap-anywhere" key={file}>
                  {file}
                </li>
              ))}
            </ul>
          </details>
          {s.origin && (
            <ImportOriginView key={value.digest} origin={s.origin} />
          )}
          <h3>
            {translate("Сообщения")} {offset + 1}–
            {Math.min(offset + 20, s.messages.length)} {translate("из")}{" "}
            {s.messages.length}
          </h3>
          {s.messages.slice(offset, offset + 20).map((m, i) => (
            <article
              className={`
                transcript-message mx-0 my-4 [&_pre]:font-mono [&_pre]:text-sm [&_pre]:leading-[1.65]
                [&_pre]:whitespace-pre-wrap [&_pre]:wrap-anywhere [&_pre]:max-h-[32rem] [&_pre]:overflow-auto
                [&_pre]:p-4 [&_pre]:bg-paper [&_pre]:border border-line
              `}
              key={offset + i}
            >
              <h4>
                {offset + i + 1}.{" "}
                {m.kind === "provenance_report"
                  ? translate("заявление источника Git AI")
                  : m.kind === "import_notice"
                    ? translate("сведения об импорте")
                    : m.role}
                {m.tool_name ? ` / ${m.tool_name}` : ""}
                {m.kind === "agent_summary"
                  ? translate(" / резюме агента")
                  : ""}
              </h4>
              <pre tabIndex={0}>
                {m.text || translate("(пустое сообщение)")}
              </pre>
            </article>
          ))}
          <div className="transcript-pages flex flex-wrap items-end gap-3 mx-0 my-4">
            {offset > 0 && (
              <button
                className={buttonStyles.secondary}
                onClick={() => setOffset(Math.max(0, offset - 20))}
              >
                {translate("Предыдущие сообщения")}
              </button>
            )}
            {offset + 20 < s.messages.length && (
              <button
                className={buttonStyles.secondary}
                onClick={() => setOffset(offset + 20)}
              >
                {translate("Следующие сообщения")}
              </button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
