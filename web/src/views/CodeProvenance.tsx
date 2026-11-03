import { useEffect, useState } from "react";
import type { Hit, SearchResult, SearchCursor } from "../domain/context.js";
import { readableError } from "../domain/context.js";
import { t } from "../domain/locale.js";
import type { ApiClient } from "../transport/api.js";
import { LatestRequest } from "../application/latest.js";
import { ErrorNotice, aborted } from "./shared.js";
import { SourceMetadata } from "./SourceMetadata.js";
import { buttonStyles } from "./ui.js";

/** Shows path reports inside exact recorded commit ranges, without asserting final line authorship. */
export function CodeProvenance({
  client,
  hit,
  onOpen,
}: {
  client: ApiClient;
  hit: Hit;
  onOpen: (hit: Hit) => void;
}) {
  const [items, setItems] = useState<Hit[]>([]);
  const [next, setNext] = useState<SearchCursor>();
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  useEffect(() => () => requests.cancel(), [requests]);
  async function load(after?: SearchCursor) {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const result = await client.request<SearchResult>(
        "/v1/context/search",
        {
          project_id: hit.source.project_id,
          repository_id: hit.source.repository_id,
          commit_sha: hit.source.commit_sha,
          file_path: hit.source.file_path,
          kinds: ["session", "code_reference"],
          limit: 20,
          ...(after ? { after } : {}),
        },
        request.signal,
      );
      if (!request.current()) return;
      if (
        result.items.some(
          (item) =>
            item.source.project_id !== hit.source.project_id ||
            item.source.repository_id !== hit.source.repository_id,
        )
      )
        throw new Error("Ответ не совпал с выбранным источником.");
      const matches = result.items.filter((item) =>
        ["tool_reported", "git_ai_reported"].includes(
          item.metadata?.relation ?? "",
        ),
      );
      setItems((old) =>
        after
          ? [
              ...old,
              ...matches.filter(
                (item) => !old.some((previous) => previous.id === item.id),
              ),
            ]
          : matches,
      );
      setNext(result.next);
      setLoaded(true);
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  const groups = new Map<string, Hit[]>();
  const nativeRecords = new Set<string>();
  for (const item of items) {
    const m = item.metadata;
    const record = m?.native_id
      ? `${item.source.session_id}/${item.source.revision}/${m.native_id}`
      : item.id;
    if (nativeRecords.has(record)) continue;
    nativeRecords.add(record);
    const key = JSON.stringify([
      m?.agent,
      m?.model,
      m?.provider,
      m?.profile,
      m?.model_basis,
      m?.attribution_kind,
    ]);
    const group = groups.get(key) ?? [];
    group.push(item);
    groups.set(key, group);
  }
  return (
    <details
      className="mt-5 border-t border-line pt-3"
      onToggle={(event) => {
        if (
          event.target === event.currentTarget &&
          event.currentTarget.open &&
          !loaded &&
          !loading
        )
          void load();
      }}
    >
      <summary className="cursor-pointer py-2 text-sm font-medium">
        {t("Связанные изменения")}
      </summary>
      <ErrorNotice>{error}</ErrorNotice>
      {loading && (
        <p className="text-sm text-muted" role="status">
          {t("Загрузка…")}
        </p>
      )}
      {loaded && !items.length && !error && (
        <p className="text-sm text-muted">
          {t("Записей об изменении файла пока нет.")}
        </p>
      )}
      <div className="space-y-3">
        {[...groups].map(([key, group]) => (
          <section key={key}>
            <div className="border-b border-line py-2">
              <SourceMetadata metadata={group[0]?.metadata} />
            </div>
            <div className="divide-y divide-line">
              {group.map((item) => (
                <button
                  className="flex min-h-10 w-full items-center justify-between gap-3 rounded-none border-0 px-0 py-2 text-left text-sm font-normal hover:text-accent"
                  key={item.id}
                  onClick={() => onOpen(item)}
                >
                  <span className="min-w-0 wrap-anywhere">
                    {item.metadata?.relation === "git_ai_reported"
                      ? t("Отчёт Git AI")
                      : item.metadata?.tool || t("Изменение файла")}
                  </span>
                  {item.metadata?.reported_line_start ? (
                    <span className="text-xs text-muted">
                      {t(
                        "Заявленные строки {0}–{1}",
                        item.metadata.reported_line_start,
                        item.metadata.reported_line_end,
                      )}
                    </span>
                  ) : /^messages\/\d+$/.test(item.part) ? (
                    <span className="text-xs text-muted">
                      #{Number(item.part.slice(9)) + 1}
                    </span>
                  ) : null}
                </button>
              ))}
            </div>
          </section>
        ))}
      </div>
      {(next || error) && (
        <button
          className={buttonStyles.text}
          disabled={loading}
          onClick={() => void load(next)}
        >
          {error ? t("Попробовать ещё раз") : t("Показать ещё")}
        </button>
      )}
    </details>
  );
}
