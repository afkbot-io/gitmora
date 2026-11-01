import { useEffect, useRef, useState } from "react";
import { t } from "../../domain/locale.js";
import type { Hit } from "../../domain/context.js";
import { Evidence } from "../Evidence.js";
import { readableError } from "../../domain/context.js";
import type {
  ContextPack,
  Selection,
  CaptureCursor,
  MRCursor,
} from "../../domain/history.js";
import { packQuery, sameSelection } from "../../domain/history.js";
import type { ApiClient } from "../../transport/api.js";
import { LatestRequest } from "../../application/latest.js";
import { ErrorNotice, aborted } from "../shared.js";
import { buttonStyles } from "../ui.js";
import { SessionTranscript } from "./SessionTranscript.js";
import { MRPanel } from "./MRPanel.js";

export function ContextDetail({
  client,
  selection,
  onNavigate,
  onBack,
}: {
  client: ApiClient;
  selection: Selection;
  onNavigate: (q: Selection) => void;
  onBack: () => void;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const [continuationsOpen, setContinuationsOpen] = useState(false);
  const [code, setCode] = useState<Hit | null>(null);
  const [pack, setPack] = useState<ContextPack | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [requests] = useState(() => new LatestRequest());
  async function load(
    pages: {
      capture_after?: CaptureCursor;
      merge_requests_after?: MRCursor;
    } = {},
  ) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    setPack(null);
    setCode(null);
    try {
      const result = await client.request<ContextPack>(
        "/v1/context/pack",
        packQuery({ ...selection, ...pages }),
        request.signal,
      );
      if (!request.current()) return;
      if (
        !sameSelection(selection, result.query) ||
        result.captures.items.some(
          (c) => c.source.project_id !== selection.project_id,
        )
      )
        throw new Error("Контекст не совпал с выбранным источником.");
      setPack(result);
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, selection, requests]);
  const captures =
    pack?.captures.items.filter(
      (c) => c.source.session_id !== selection.session_id,
    ) ?? [];
  return (
    <section className="context-detail min-w-0">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <button className={buttonStyles.text} onClick={onBack}>
          {t("Назад к поиску")}
        </button>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {t("Обновить")}
        </button>
      </div>
      <h1 ref={heading} tabIndex={-1} className="mb-5">
        {selection.merge_request_iid
          ? t("Контекст MR/PR")
          : selection.commit_sha
            ? t("Контекст коммита")
            : t("Сессия агента")}
      </h1>
      {selection.commit_sha && (
        <code className="mb-5 block wrap-anywhere text-sm text-muted">
          {selection.commit_sha}
        </code>
      )}
      <ErrorNotice>{error}</ErrorNotice>
      {loading && (
        <p role="status" className="text-sm text-muted">
          {t("Загрузка…")}
        </p>
      )}
      {pack && (
        <div className="grid gap-5">
          {selection.merge_request_iid && !pack.merge_requests.items.length && (
            <p className="rounded-lg border border-line bg-paper p-5 text-sm text-muted">
              {t("MR/PR пока недоступен. Проверьте подключение репозитория.")}
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
          {selection.session_id && (
            <SessionTranscript
              client={client}
              selection={selection}
              onNavigate={onNavigate}
            />
          )}
          {!selection.session_id && (
            <section
              className="rounded-lg border border-line bg-paper p-5"
              aria-label={t("Связанные сессии")}
            >
              <h2 className="mb-3 text-base">{t("Связанные сессии")}</h2>
              {pack.captures.omitted_for_permissions ? (
                <p className="mb-0 text-sm text-muted">
                  {t("Нет доступа к сессиям.")}
                </p>
              ) : !captures.length ? (
                <p className="mb-0 text-sm text-muted">
                  {t("Для этих коммитов ещё нет записанных сессий.")}
                </p>
              ) : (
                captures.map((c) => (
                  <button
                    key={`${c.source.repository_id}/${c.source.session_id}`}
                    className="flex w-full justify-between gap-3 rounded-none border-b border-line px-0 py-3 text-left hover:text-accent"
                    onClick={() =>
                      onNavigate({
                        project_id: selection.project_id,
                        repository_id: c.source.repository_id!,
                        session_id: c.source.session_id!,
                      })
                    }
                  >
                    <span className="min-w-0 truncate text-sm">
                      {c.agent}{" "}
                      <span className="text-muted">
                        {t("Сегмент {0}", c.segment)}
                      </span>
                    </span>
                    <span className="shrink-0 text-xs text-muted">
                      {t("Открыть сессию")}
                    </span>
                  </button>
                ))
              )}
              {pack.captures.next && (
                <button
                  className={buttonStyles.text + " mt-3"}
                  onClick={() =>
                    void load({ capture_after: pack.captures.next! })
                  }
                >
                  {t("Следующие сессии")}
                </button>
              )}
            </section>
          )}
          {selection.session_id &&
            (captures.length > 0 || pack.captures.next) && (
              <details
                open={continuationsOpen}
                onToggle={(event) =>
                  setContinuationsOpen(event.currentTarget.open)
                }
                className="border-t border-line pt-3 text-sm"
              >
                <summary className="py-2">{t("Продолжение сессии")}</summary>
                {captures.map((c) => (
                  <button
                    key={c.source.session_id}
                    className={buttonStyles.text + " block my-2"}
                    onClick={() =>
                      onNavigate({
                        project_id: selection.project_id,
                        repository_id: c.source.repository_id!,
                        session_id: c.source.session_id!,
                      })
                    }
                  >
                    {c.agent} · {t("Сегмент {0}", c.segment)}
                  </button>
                ))}
                {pack.captures.next && (
                  <button
                    className={buttonStyles.text + " mt-3"}
                    onClick={() =>
                      void load({ capture_after: pack.captures.next! })
                    }
                  >
                    {t("Следующие сессии")}
                  </button>
                )}
              </details>
            )}
          {!selection.session_id &&
            pack.evidence.some((e) => e.kind === "code") && (
              <section className="overflow-hidden rounded-lg border border-line bg-paper">
                <h2 className="border-b border-line px-5 py-3 text-base">
                  {t("Код коммита")}
                </h2>
                <div className="flex flex-wrap gap-2 px-5 pb-3">
                  {pack.evidence
                    .filter((e) => e.kind === "code")
                    .map((e) => (
                      <button
                        key={e.id}
                        className={buttonStyles.text + " wrap-anywhere"}
                        onClick={() => setCode(e)}
                      >
                        {e.source.file_path}
                      </button>
                    ))}
                </div>
                {code && <Evidence key={code.id} client={client} hit={code} />}
              </section>
            )}
          {pack.merge_requests.next && (
            <button
              className={buttonStyles.text}
              onClick={() =>
                void load({ merge_requests_after: pack.merge_requests.next! })
              }
            >
              {t("Следующие MR")}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
