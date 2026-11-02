import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { t } from "../../domain/locale.js";
import type {
  CaptureWindow,
  CaptureCursor,
  ContextPack,
  Selection,
  SessionRevision,
} from "../../domain/history.js";
import {
  conversationItems,
  transcriptLocation,
} from "../../domain/conversation.js";
import type { ApiClient } from "../../transport/api.js";
import { readableError } from "../../domain/context.js";
import { packQuery, sameSelection } from "../../domain/history.js";
import { messageBatchSize } from "../../application/transcript.js";
import { PreviousMessages } from "./PreviousMessages.js";
import { LatestRequest } from "../../application/latest.js";
import { ErrorNotice, aborted, date } from "../shared.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { ImportOriginView } from "./ImportOrigin.js";
import { ConversationMessage } from "./ConversationMessage.js";

interface Part {
  value: SessionRevision;
  visible: number;
  segment: number;
}

/** Fetch one bounded, exact revision at a time. Appending never replaces earlier messages. */
export function SessionTranscript({
  client,
  selection,
  windows,
  captureAfter,
  onNavigate,
}: {
  client: ApiClient;
  selection: Selection;
  windows: CaptureWindow[];
  captureAfter?: CaptureCursor;
  onNavigate: (q: Selection) => void;
}) {
  const [parts, setParts] = useState<Part[]>([]);
  const [revision, setRevision] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [dialogueOnly, setDialogueOnly] = useState(false);
  const [query, setQuery] = useState("");
  const [requests] = useState(() => new LatestRequest());
  const [locator] = useState(() => transcriptLocation(location.hash));
  const target = useRef<number | null>(null);
  const anchor = useRef<number | null>(null);
  const catalog = useRef(windows);
  const catalogAfter = useRef(captureAfter);
  const busy = useRef(false);
  useLayoutEffect(() => {
    if (target.current !== null) {
      const node = document.querySelector<HTMLElement>(
        `.transcript [data-message-index="${target.current + 1}"]`,
      );
      if (node) {
        let parent = node.parentElement?.closest("details");
        while (parent) {
          parent.open = true;
          parent = parent.parentElement?.closest("details");
        }
        if (node instanceof HTMLDetailsElement) node.open = true;
        node.scrollIntoView({ block: "center" });
      }
      target.current = null;
    } else if (anchor.current !== null) {
      window.scrollTo({ top: anchor.current, behavior: "instant" });
      anchor.current = null;
    }
  }, [parts]);
  async function read(
    session: string,
    number: number,
    signal: AbortSignal,
    digest?: string,
  ) {
    const result = await client.request<SessionRevision>(
      `/v1/repositories/${selection.repository_id}/sessions/${session}`,
      undefined,
      signal,
      number ? { revision: number } : undefined,
    );
    if (
      result.snapshot.repository_id !== selection.repository_id ||
      result.snapshot.session_id !== session ||
      (number && result.snapshot.revision !== number) ||
      (digest && result.digest !== digest)
    )
      throw new Error("Ответ не совпал с выбранной сессией и ревизией.");
    return result;
  }
  async function load(number = 0) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const value = await read(selection.session_id!, number, request.signal);
      if (!request.current()) return;
      const part: Part = {
        value,
        visible: Math.min(
          value.snapshot.messages.length,
          Math.max(20, locator.message !== null ? locator.message + 1 : 20),
        ),
        segment:
          windows.find((w) => w.source.session_id === selection.session_id)
            ?.segment ??
          value.snapshot.continuation?.segment ??
          1,
      };
      const updated = await fill(
        [part],
        Math.max(0, messageBatchSize - part.visible),
        request,
      );
      if (!updated || !request.current()) return;
      setParts(updated);
      target.current = locator.message;
      setRevision(String(value.snapshot.revision));
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load(locator.revision);
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
  const first = parts[0]?.value;
  const last = parts.at(-1);
  const nextWindow = (items: CaptureWindow[], value = last?.value) =>
    items.find(
      (w) =>
        w.chain_verified &&
        w.source.repository_id === selection.repository_id &&
        w.continuation?.previous_session_id === value?.snapshot.session_id &&
        w.continuation?.previous_revision === value?.snapshot.revision &&
        w.continuation?.previous_digest === value?.digest,
    );
  const hasMore =
    !!last &&
    (last.visible < last.value.snapshot.messages.length ||
      !!nextWindow(catalog.current) ||
      !!catalogAfter.current);
  async function following(value: SessionRevision, signal: AbortSignal) {
    let next = nextWindow(catalog.current, value);
    const seen = new Set<string>();
    for (let page = 0; !next && catalogAfter.current && page < 43; page++) {
      const cursor = catalogAfter.current;
      const key = JSON.stringify(cursor);
      if (seen.has(key))
        throw new Error("Контекст не совпал с выбранным источником.");
      seen.add(key);
      const result = await client.request<ContextPack>(
        "/v1/context/pack",
        packQuery({ ...selection, capture_after: cursor }),
        signal,
      );
      if (
        !sameSelection(selection, result.query) ||
        result.captures.items.some(
          (item) =>
            item.source.project_id !== selection.project_id ||
            item.source.repository_id !== selection.repository_id,
        )
      )
        throw new Error("Контекст не совпал с выбранным источником.");
      catalog.current = [
        ...catalog.current,
        ...result.captures.items.filter(
          (item) =>
            !catalog.current.some(
              (old) => old.source.session_id === item.source.session_id,
            ),
        ),
      ];
      catalogAfter.current = result.captures.next;
      next = nextWindow(catalog.current, value);
    }
    if (!next && catalogAfter.current)
      throw new Error(
        "Не удалось загрузить полную запись. Попробуйте ещё раз.",
      );
    return next;
  }
  async function fill(
    initial: Part[],
    remaining: number,
    request: ReturnType<LatestRequest["begin"]>,
  ) {
    const updated = initial.map((part) => ({ ...part }));
    for (
      let window = 0;
      window <= messageBatchSize && remaining > 0;
      window++
    ) {
      const part = updated.at(-1)!;
      const take = Math.min(
        remaining,
        part.value.snapshot.messages.length - part.visible,
      );
      part.visible += take;
      remaining -= take;
      if (!remaining) break;
      const next = await following(part.value, request.signal);
      if (!request.current()) return null;
      if (!next) break;
      if (
        updated.some(
          (old) => old.value.snapshot.session_id === next.source.session_id,
        )
      )
        throw new Error("Контекст не совпал с выбранным источником.");
      const value = await read(
        next.source.session_id!,
        next.source.revision!,
        request.signal,
        next.source.digest,
      );
      if (!request.current()) return null;
      updated.push({ value, visible: 0, segment: next.segment });
    }
    if (!request.current()) return null;
    return updated;
  }
  async function append() {
    if (!last || busy.current) return;
    busy.current = true;
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const updated = await fill(parts, messageBatchSize, request);
      if (!updated || !request.current()) return;
      anchor.current = window.scrollY;
      setParts(updated);
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      busy.current = false;
      if (request.current()) setLoading(false);
    }
  }
  const shown = parts.reduce(
    (n, p) => n + Math.min(p.visible, p.value.snapshot.messages.length),
    0,
  );
  return (
    <section className="transcript min-w-0" aria-label={t("Запись сессии")}>
      <header className="mb-5 flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div>
          <h2 className="mb-1">{t("Диалог с агентом")}</h2>
          {first && (
            <p className="mb-0 text-xs text-muted">
              {first.snapshot.agent.name} · {date(first.snapshot.started_at)}
              {first.snapshot.agent.model &&
              !["unknown", "unreported", "no-model-needed"].includes(
                first.snapshot.agent.model,
              )
                ? ` · ${first.snapshot.agent.model}`
                : ""}
            </p>
          )}
        </div>
        <span className="text-xs text-muted" role="status">
          {t("Загружено сообщений: {0}", shown)}
        </span>
      </header>
      <ErrorNotice>{error}</ErrorNotice>
      {!first && loading && <p role="status">{t("Читаем запись сессии…")}</p>}
      {first && (
        <>
          <div className="mb-5 flex flex-wrap items-center gap-3">
            <label className="m-0 min-w-40 flex-1">
              <span className="sr-only">
                {t("Найти в загруженных сообщениях")}
              </span>
              <input
                className="w-full text-sm"
                type="search"
                placeholder={t("Найти в загруженных сообщениях")}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <label className="m-0 flex min-h-11 items-center gap-2 text-sm font-normal">
              <input
                type="checkbox"
                checked={dialogueOnly}
                onChange={(e) => setDialogueOnly(e.target.checked)}
              />
              {t("Только переписка")}
            </label>
          </div>
          <PreviousMessages
            key={`${first.snapshot.session_id}/${first.snapshot.revision}/${first.digest}`}
            client={client}
            project={selection.project_id}
            onlyDialogue={dialogueOnly}
            query={query}
            before={{
              repository_id: first.snapshot.repository_id,
              session_id: first.snapshot.session_id,
              revision: first.snapshot.revision,
              digest: first.digest,
              segment: first.snapshot.continuation?.segment ?? 1,
              offset: 0,
            }}
          />
          <div className="conversation-timeline mx-auto grid w-full max-w-240 gap-5">
            {parts.map((part, i) => {
              const items = conversationItems(
                part.value.snapshot.messages,
                part.visible,
                dialogueOnly,
                query,
              );
              return (
                <section
                  className="conversation-part min-w-0 space-y-4"
                  key={part.value.snapshot.session_id}
                  aria-label={t("Часть {0}", part.segment)}
                >
                  {i > 0 && (
                    <h3 className="my-6 border-y border-line py-3 text-center text-xs font-normal text-muted">
                      {t("Продолжение диалога · часть {0}", part.segment)}
                    </h3>
                  )}
                  {items.map((item) => (
                    <ConversationMessage key={item.key} item={item} />
                  ))}
                  {!items.length && (
                    <p className="text-sm text-muted">
                      {query
                        ? t("В этой части совпадений нет.")
                        : t("В этой части только действия инструментов.")}
                    </p>
                  )}
                </section>
              );
            })}
            {hasMore && (
              <div className="border-t border-line pt-4">
                <button
                  className={buttonStyles.secondary + " w-full"}
                  disabled={loading}
                  onClick={() => void append()}
                >
                  {loading ? t("Загрузка…") : t("Загрузить ещё 20 сообщений")}
                </button>
                <p className="mt-2 mb-0 text-center text-xs text-muted">
                  {t("Новые сообщения появятся ниже.")}
                </p>
              </div>
            )}
          </div>
          <details className="session-source mt-8 border-t border-line pt-3 text-sm">
            <summary className="cursor-pointer py-2 text-muted">
              {t("Сведения об источнике")}
            </summary>
            <dl className={sharedStyles.sourceDetails}>
              <dt>{t("Сессия")}</dt>
              <dd className="font-mono">{first.snapshot.session_id}</dd>
              <dt>{t("Ревизия")}</dt>
              <dd>{first.snapshot.revision}</dd>
              <dt>Digest</dt>
              <dd className="font-mono">{first.digest}</dd>
            </dl>
            <form
              onSubmit={select}
              className="my-4 flex flex-wrap items-end gap-3"
            >
              <label className="m-0 max-w-40">
                {t("Ревизия сессии")}
                <input
                  className="mt-2 w-full"
                  type="number"
                  min="1"
                  max="1000000000"
                  required
                  value={revision}
                  onChange={(e) => setRevision(e.target.value)}
                />
              </label>
              <button className={buttonStyles.secondary} disabled={loading}>
                {t("Открыть версию")}
              </button>
              <button
                type="button"
                className={buttonStyles.secondary}
                disabled={loading}
                onClick={() => void load()}
              >
                {t("Последняя версия")}
              </button>
            </form>
            <details className="my-3">
              <summary className="py-2">
                {t("Коммиты, указанные агентом:")}{" "}
                {first.snapshot.commits.length}
              </summary>
              <div className="flex flex-wrap gap-2">
                {first.snapshot.commits.map((sha) => (
                  <button
                    key={sha}
                    className={buttonStyles.text + " font-mono"}
                    title={sha}
                    onClick={() =>
                      onNavigate({
                        project_id: selection.project_id,
                        repository_id: first.snapshot.repository_id,
                        commit_sha: sha,
                      })
                    }
                  >
                    {sha.slice(0, 10)} ·{" "}
                    {first.confirmed_commits.includes(sha)
                      ? t("Подтверждён провайдером")
                      : t("Коммит ещё не проверен")}
                  </button>
                ))}
              </div>
            </details>
            <details className="my-3">
              <summary className="py-2">
                {t("Пути, указанные агентом:")} {first.snapshot.files.length}
              </summary>
              <ul className="pl-5 font-mono text-xs wrap-anywhere">
                {first.snapshot.files.map((file) => (
                  <li key={file}>{file}</li>
                ))}
              </ul>
            </details>
            {first.snapshot.origin && (
              <ImportOriginView
                key={first.digest}
                origin={first.snapshot.origin}
              />
            )}
          </details>
        </>
      )}
    </section>
  );
}
