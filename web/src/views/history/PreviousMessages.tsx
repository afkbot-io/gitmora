import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { previousMessageBatch } from "../../application/transcript.js";
import { scopedConversationItems } from "../../domain/conversation.js";
import type {
  MessageCursor,
  ScopedMessage,
  SessionRevision,
} from "../../domain/history.js";
import { t } from "../../domain/locale.js";
import { readableError } from "../../domain/context.js";
import type { ApiClient } from "../../transport/api.js";
import { aborted, ErrorNotice } from "../shared.js";
import { buttonStyles } from "../ui.js";
import { ConversationMessage } from "./ConversationMessage.js";

/** Explicit older session history never changes the surrounding MR/commit selection. */
export function PreviousMessages({
  client,
  project,
  before,
  scoped = false,
  onlyDialogue = false,
  query = "",
}: {
  client: ApiClient;
  project: string;
  before: MessageCursor;
  scoped?: boolean;
  onlyDialogue?: boolean;
  query?: string;
}) {
  const [cursor, setCursor] = useState<MessageCursor | undefined>(before);
  const [items, setItems] = useState<ScopedMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const busy = useRef(false);
  const mounted = useRef(true);
  const controller = useRef<AbortController | null>(null);
  const cache = useRef(new Map<string, SessionRevision>());
  const root = useRef<HTMLElement>(null);
  const anchor = useRef<number | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.current?.abort();
    };
  }, []);
  useLayoutEffect(() => {
    if (anchor.current === null || !root.current) return;
    window.scrollBy({
      top: root.current.getBoundingClientRect().bottom - anchor.current,
      behavior: "instant",
    });
    anchor.current = null;
  }, [items]);
  async function prepend() {
    if (!cursor || busy.current) return;
    busy.current = true;
    setLoading(true);
    setError("");
    const request = new AbortController();
    controller.current = request;
    try {
      const result = await previousMessageBatch(
        project,
        cursor,
        async (position) => {
          const key = `${position.repository_id}/${position.session_id}/${position.revision}/${position.digest}`;
          const existing = cache.current.get(key);
          if (existing) return existing;
          const value = await client.request<SessionRevision>(
            `/v1/repositories/${position.repository_id}/sessions/${position.session_id}`,
            undefined,
            request.signal,
            { revision: position.revision },
          );
          cache.current.set(key, value);
          if (cache.current.size > 2) {
            const oldest = cache.current.keys().next().value;
            if (oldest) cache.current.delete(oldest);
          }
          return value;
        },
      );
      if (!mounted.current) return;
      anchor.current = root.current?.getBoundingClientRect().bottom ?? null;
      setItems((current) => [...result.items, ...current]);
      setCursor(result.before);
    } catch (e) {
      if (mounted.current && !aborted(e)) setError(readableError(e));
    } finally {
      busy.current = false;
      if (mounted.current) setLoading(false);
    }
  }
  const groups = new Map<string, ScopedMessage[]>();
  for (const item of items) {
    const key = `${item.source.session_id}/${item.source.revision}`;
    const group = groups.get(key) ?? [];
    group.push(item);
    groups.set(key, group);
  }
  const available = cursor && (cursor.offset > 0 || cursor.segment > 1);
  if (!available && !items.length && !error) return null;
  return (
    <section
      ref={root}
      className="previous-messages flow-root mx-auto w-full max-w-240 min-w-0"
      aria-label={t("Предыдущие сообщения сессии")}
    >
      {available && (
        <button
          className={buttonStyles.secondary + " mb-4 w-full"}
          disabled={loading}
          onClick={() => void prepend()}
        >
          {loading ? t("Загрузка…") : t("Загрузить предыдущие 20 сообщений")}
        </button>
      )}
      <ErrorNotice>{error}</ErrorNotice>
      {items.length > 0 && (
        <>
          <div className="space-y-4">
            {[...groups].map(([key, messages]) => (
              <section
                key={key}
                data-message-session={messages[0]?.source.session_id}
                className="space-y-4 min-w-0"
              >
                {scopedConversationItems(messages, onlyDialogue, query).map(
                  (item) => (
                    <ConversationMessage key={item.key} item={item} />
                  ),
                )}
              </section>
            ))}
          </div>
          <p className="my-6 border-y border-line py-3 text-center text-xs text-muted">
            {scoped
              ? t("Выше — предыдущая переписка, вне выбранного изменения")
              : t("Предыдущие сообщения сессии")}
          </p>
        </>
      )}
    </section>
  );
}
