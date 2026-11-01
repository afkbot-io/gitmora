import { Suspense, lazy, useEffect, useRef, useState } from "react";
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
import { groupConversations } from "../../domain/conversation.js";
import { packQuery, sameSelection } from "../../domain/history.js";
import type { ApiClient } from "../../transport/api.js";
import { LatestRequest } from "../../application/latest.js";
import { ErrorNotice, aborted } from "../shared.js";
import { buttonStyles } from "../ui.js";
import { MRPanel } from "./MRPanel.js";
const ScopedTranscript = lazy(() =>
  import("./ScopedTranscript.js").then((module) => ({
    default: module.ScopedTranscript,
  })),
);
const SessionTranscript = lazy(() =>
  import("./SessionTranscript.js").then((module) => ({
    default: module.SessionTranscript,
  })),
);

export function ContextDetail({
  client,
  selection,
  provider,
  onNavigate,
  onBack,
}: {
  client: ApiClient;
  selection: Selection;
  provider: "github" | "gitlab";
  onNavigate: (q: Selection, fromMR?: number) => void;
  onBack: () => void;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
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
    propagateError = false,
  ) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    const append = !!(pages.capture_after || pages.merge_requests_after);
    if (!append) {
      setPack(null);
      setCode(null);
    }
    try {
      let result = await client.request<ContextPack>(
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
      if (
        append &&
        pack?.merge_requests.items.some((old) =>
          result.merge_requests.items.some(
            (next) =>
              old.repository_id === next.repository_id &&
              old.iid === next.iid &&
              old.observed_head_sha !== next.observed_head_sha,
          ),
        )
      )
        throw new Error("Состав MR/PR изменился. Обновите контекст.");
      if (!append && !selection.session_id && !selection.dialogue_id) {
        let pages = 0;
        while (result.captures.next && pages++ < 43) {
          const page = await client.request<ContextPack>(
            "/v1/context/pack",
            packQuery({ ...selection, capture_after: result.captures.next }),
            request.signal,
          );
          if (!request.current()) return;
          if (!sameSelection(selection, page.query))
            throw new Error("Контекст не совпал с выбранным источником.");
          if (
            result.merge_requests.items.some((old) =>
              page.merge_requests.items.some(
                (next) =>
                  old.iid === next.iid &&
                  old.repository_id === next.repository_id &&
                  old.observed_head_sha !== next.observed_head_sha,
              ),
            )
          )
            throw new Error("Состав MR/PR изменился. Обновите контекст.");
          result = {
            ...result,
            captures: {
              ...page.captures,
              items: [...result.captures.items, ...page.captures.items],
            },
            scope: {
              kind: result.scope?.kind ?? "",
              missing_commit_ranges:
                !!result.scope?.missing_commit_ranges ||
                !!page.scope?.missing_commit_ranges,
            },
          };
        }
      }
      const merged =
        append && pack
          ? {
              ...result,
              captures: pages.capture_after
                ? {
                    ...result.captures,
                    items: [
                      ...pack.captures.items,
                      ...result.captures.items.filter(
                        (c) =>
                          !pack.captures.items.some(
                            (old) =>
                              old.source.repository_id ===
                                c.source.repository_id &&
                              old.source.session_id === c.source.session_id,
                          ),
                      ),
                    ],
                  }
                : pack.captures,
              merge_requests: pages.merge_requests_after
                ? {
                    ...result.merge_requests,
                    items: [
                      ...pack.merge_requests.items,
                      ...result.merge_requests.items.filter(
                        (m) =>
                          !pack.merge_requests.items.some(
                            (old) =>
                              old.repository_id === m.repository_id &&
                              old.iid === m.iid,
                          ),
                      ),
                    ],
                  }
                : pack.merge_requests,
              evidence: pack.evidence,
            }
          : result;
      setPack(merged);
      return merged.captures.items;
    } catch (e) {
      if (!aborted(e) && request.current()) setError(readableError(e));
      if (propagateError) throw e;
      return pack?.captures.items ?? [];
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, selection, requests]);
  const parentMR =
    selection.merge_request_iid ??
    Number(new URLSearchParams(location.hash.slice(1)).get("from_mr"));
  const mrLabel = (provider === "github" ? "PR #" : "MR !") + parentMR;
  const groups = groupConversations(
    (pack?.captures.items ?? []).filter(
      (c) => c.range_status !== "unavailable",
    ),
  );
  const legacyGroups = groupConversations(
    (pack?.captures.items ?? []).filter(
      (c) => c.range_status === "unavailable",
    ),
  );
  return (
    <section className="context-detail min-w-0">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <nav
          aria-label={t("Навигация")}
          className="flex flex-wrap items-center gap-2 text-sm text-muted"
        >
          <button className={buttonStyles.text} onClick={onBack}>
            {t("Поиск")}
          </button>
          {(selection.merge_request_iid ||
            Number(
              new URLSearchParams(location.hash.slice(1)).get("from_mr"),
            )) > 0 && (
            <>
              <span aria-hidden="true">/</span>
              <button
                className={buttonStyles.text}
                disabled={!selection.dialogue_id && !selection.commit_sha}
                onClick={() =>
                  onNavigate({
                    project_id: selection.project_id,
                    repository_id: selection.repository_id!,
                    merge_request_iid:
                      selection.merge_request_iid ??
                      Number(
                        new URLSearchParams(location.hash.slice(1)).get(
                          "from_mr",
                        ),
                      ),
                  })
                }
              >
                {mrLabel}
              </button>
            </>
          )}
          {(selection.session_id || selection.dialogue_id) && (
            <>
              <span aria-hidden="true">/</span>
              <span aria-current="page">
                {selection.session_id ? t("Полная история") : t("Диалог")}
              </span>
            </>
          )}
        </nav>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {t("Обновить")}
        </button>
      </div>
      <h1 ref={heading} tabIndex={-1} className="mb-5">
        {selection.dialogue_id
          ? selection.merge_request_iid
            ? t("Диалог {0}", mrLabel)
            : t("Диалог по коммиту {0}", selection.commit_sha?.slice(0, 10))
          : selection.merge_request_iid
            ? mrLabel
            : selection.commit_sha
              ? t("Контекст коммита")
              : t("Полная история диалога")}
      </h1>
      {selection.commit_sha && (
        <code className="mb-5 block wrap-anywhere text-sm text-muted">
          {selection.commit_sha}
        </code>
      )}
      <ErrorNotice>{error}</ErrorNotice>
      {loading && !pack && (
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
          {!selection.dialogue_id &&
            pack.merge_requests.items
              .filter((m) => !parentMR || m.iid === parentMR)
              .map((m) => (
                <MRPanel
                  key={`${m.repository_id}/${m.iid}`}
                  mr={m}
                  project={selection.project_id}
                  onNavigate={(q) => onNavigate(q, selection.merge_request_iid)}
                />
              ))}
          {pack.scope?.missing_commit_ranges && (
            <p className="mb-0 text-sm text-muted">
              {t("Для части коммитов границы переписки не записаны.")}
            </p>
          )}
          {selection.dialogue_id && (
            <button
              className={buttonStyles.text}
              onClick={() =>
                onNavigate({
                  project_id: selection.project_id,
                  repository_id:
                    pack.captures.items[0]?.source.repository_id ??
                    selection.repository_id!,
                  session_id: selection.dialogue_id!,
                })
              }
            >
              {t("Открыть полную историю отдельно")}
            </button>
          )}
          {selection.dialogue_id && (
            <Suspense
              fallback={<p role="status">{t("Читаем запись сессии…")}</p>}
            >
              <ScopedTranscript
                key={JSON.stringify(selection)}
                client={client}
                selection={selection}
                initial={pack}
              />
            </Suspense>
          )}
          {selection.session_id && (
            <Suspense
              fallback={<p role="status">{t("Читаем запись сессии…")}</p>}
            >
              <SessionTranscript
                client={client}
                selection={selection}
                windows={pack.captures.items}
                moreWindows={!!pack.captures.next}
                onMoreWindows={async () =>
                  pack.captures.next
                    ? ((await load(
                        { capture_after: pack.captures.next },
                        true,
                      )) ?? pack.captures.items)
                    : pack.captures.items
                }
                onNavigate={onNavigate}
              />
            </Suspense>
          )}
          {!selection.session_id && !selection.dialogue_id && (
            <section className="min-w-0" aria-label={t("Связанные сессии")}>
              <h2 className="mb-4 text-base">
                {t("Диалоги к этому изменению")}
              </h2>
              {pack.captures.omitted_for_permissions ? (
                <p className="text-sm text-muted">
                  {t("Нет доступа к сессиям.")}
                </p>
              ) : !groups.length ? (
                <p className="rounded-lg border border-line bg-paper p-5 text-sm text-muted">
                  {pack.scope?.missing_commit_ranges
                    ? t("Для этого изменения границы переписки не записаны.")
                    : t("Для этих коммитов ещё нет записанных сессий.")}
                </p>
              ) : (
                <div className="grid grid-cols-2 gap-3 compact:grid-cols-1">
                  {groups.map((group) => (
                    <button
                      key={group.key}
                      className="conversation-card flex min-w-0 flex-col items-stretch justify-start gap-3 rounded-lg border border-line bg-paper p-4 text-left hover:border-accent"
                      onClick={() =>
                        onNavigate({
                          project_id: selection.project_id,
                          repository_id: group.first.source.repository_id!,
                          ...(selection.merge_request_iid ||
                          selection.commit_sha
                            ? {
                                ...selection,
                                dialogue_id: group.first.chain_verified
                                  ? group.first.root_session_id
                                  : group.first.source.session_id!,
                                ...(selection.merge_request_iid &&
                                pack.merge_requests.items[0]
                                  ? {
                                      expected_head_sha:
                                        pack.merge_requests.items[0]
                                          .observed_head_sha,
                                    }
                                  : {}),
                              }
                            : { session_id: group.first.source.session_id! }),
                        })
                      }
                    >
                      <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs font-normal text-muted">
                        <span className="font-medium text-ink">
                          {group.first.agent}
                        </span>
                      </span>
                      <span className="min-w-0 text-sm leading-6 wrap-anywhere line-clamp-3">
                        {t("Обсуждение этого изменения")}
                      </span>
                      <span className="text-xs font-medium text-accent">
                        {t("Читать диалог")}
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </section>
          )}
          {!selection.session_id &&
            !selection.dialogue_id &&
            legacyGroups.length > 0 && (
              <details className="border-t border-line pt-3 text-sm">
                <summary className="cursor-pointer py-2 text-muted">
                  {t("Полная история")}
                </summary>
                <p className="mt-2 text-xs text-muted">
                  {t("Эти записи могут включать другие изменения.")}
                </p>
                <div className="flex flex-wrap gap-3">
                  {legacyGroups.map((group) => (
                    <button
                      key={group.key}
                      className={buttonStyles.text}
                      onClick={() =>
                        onNavigate({
                          project_id: selection.project_id,
                          repository_id: group.first.source.repository_id!,
                          session_id: group.first.source.session_id!,
                        })
                      }
                    >
                      {group.first.agent} · {t("Открыть отдельно")}
                    </button>
                  ))}
                </div>
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
          {!selection.session_id &&
            !selection.dialogue_id &&
            pack.merge_requests.next && (
              <button
                className={buttonStyles.text}
                disabled={loading}
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
