import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { t } from "../../domain/locale.js";
import {
  packQuery,
  sameSelection,
  type ContextPack,
  type Selection,
  type ScopedMessage,
} from "../../domain/history.js";
import type { IndexedMessage, Message } from "../../domain/conversation.js";
import { scopedConversationItems } from "../../domain/conversation.js";
import { messageBatch } from "../../application/transcript.js";
import { PreviousMessages } from "./PreviousMessages.js";
import { readableError } from "../../domain/context.js";
import type { ApiClient } from "../../transport/api.js";
import { ConversationMessage } from "./ConversationMessage.js";
import { ErrorNotice } from "../shared.js";
import { buttonStyles } from "../ui.js";

export function ScopedTranscript({
  client,
  selection,
  initial,
}: {
  client: ApiClient;
  selection: Selection;
  initial: ContextPack;
}) {
  const [items, setItems] = useState(initial.transcript?.items ?? []);
  const [next, setNext] = useState(initial.transcript?.next);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [onlyDialogue, setOnlyDialogue] = useState(false);
  const [query, setQuery] = useState("");
  const anchor = useRef<number | null>(null);
  const busy = useRef(false);
  const batchController = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const controllers = useRef(new Set<AbortController>());
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      for (const c of controllers.current) c.abort();
    };
  }, []);
  useLayoutEffect(() => {
    if (anchor.current !== null) {
      window.scrollTo({ top: anchor.current, behavior: "instant" });
      anchor.current = null;
    }
  }, [items]);
  async function readFull(entry: IndexedMessage): Promise<Message> {
    if (!entry.fullTextAfter)
      throw new Error("Контекст не совпал с выбранным источником.");
    const controller = new AbortController();
    controllers.current.add(controller);
    let cursor = entry.fullTextAfter;
    let text = "";
    try {
      for (let page = 0; page < 32; page++) {
        const query = {
          ...selection,
          message_view: "full" as const,
          message_limit: 1,
          message_after: cursor,
        };
        const result = await client.request<ContextPack>(
          "/v1/context/pack",
          packQuery(query),
          controller.signal,
        );
        if (!sameSelection(query, result.query))
          throw new Error("Контекст не совпал с выбранным источником.");
        const item = result.transcript?.items.find(
          (m) =>
            m.source.session_id === entry.fullTextAfter!.session_id &&
            m.index === entry.index,
        );
        if (
          !item ||
          (item.message_offset ?? 0) !== Array.from(text).length ||
          item.source.revision !== entry.fullTextAfter.revision ||
          item.source.digest !== entry.fullTextAfter.digest
        )
          throw new Error("Контекст не совпал с выбранным источником.");
        text += item.message.text;
        if (!item.text_truncated) return { ...item.message, text };
        if (!result.transcript?.next) break;
        cursor = result.transcript.next;
      }
      throw new Error(
        "Не удалось загрузить полную запись. Попробуйте ещё раз.",
      );
    } finally {
      controllers.current.delete(controller);
    }
  }
  async function append(initialPage?: ContextPack) {
    if (!initialPage && (!next || busy.current)) return;
    if (initialPage) batchController.current?.abort();
    busy.current = true;
    setLoading(true);
    setError("");
    const controller = new AbortController();
    batchController.current = controller;
    controllers.current.add(controller);
    try {
      const result = await messageBatch(
        selection,
        (query) =>
          client.request<ContextPack>(
            "/v1/context/pack",
            query,
            controller.signal,
          ),
        initialPage ? undefined : next,
        initialPage,
      );
      if (!mounted.current || batchController.current !== controller) return;
      if (!initialPage) anchor.current = window.scrollY;
      setItems((current) =>
        initialPage ? result.items : [...current, ...result.items],
      );
      setNext(result.next);
    } catch (e) {
      if (
        mounted.current &&
        batchController.current === controller &&
        !(e instanceof DOMException && e.name === "AbortError")
      )
        setError(readableError(e));
    } finally {
      controllers.current.delete(controller);
      if (batchController.current === controller) {
        busy.current = false;
        if (mounted.current) setLoading(false);
      }
    }
  }
  useEffect(() => {
    void append(initial);
  }, [client, selection, initial]);
  const parts = new Map<string, ScopedMessage[]>();
  for (const item of items) {
    const key = `${item.source.repository_id}/${item.source.session_id}`;
    const value = parts.get(key) ?? [];
    value.push(item);
    parts.set(key, value);
  }
  return (
    <section
      className="scoped-transcript min-w-0"
      aria-label={t("Диалог изменения")}
    >
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <label className="m-0 min-w-40 flex-1">
          <span className="sr-only">{t("Найти в загруженных сообщениях")}</span>
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
            checked={onlyDialogue}
            onChange={(e) => setOnlyDialogue(e.target.checked)}
          />
          {t("Только переписка")}
        </label>
      </div>
      {items[0] && (
        <PreviousMessages
          key={`${items[0].source.session_id}/${items[0].source.revision}/${items[0].index}`}
          client={client}
          project={selection.project_id}
          scoped
          onlyDialogue={onlyDialogue}
          query={query}
          before={{
            repository_id: items[0].source.repository_id!,
            session_id: items[0].source.session_id!,
            revision: items[0].source.revision!,
            digest: items[0].source.digest,
            segment: items[0].segment,
            offset: items[0].index,
          }}
        />
      )}
      <div role="status" className="mx-auto mb-4 max-w-240 text-xs text-muted">
        {t("Загружено сообщений: {0}", items.length)}
      </div>
      <div className="scoped-current mx-auto grid max-w-240 gap-4">
        {[...parts].map(([id, messages]) => (
          <section key={id} className="min-w-0 space-y-4">
            {scopedConversationItems(messages, onlyDialogue, query).map(
              (item) => (
                <ConversationMessage
                  key={item.key}
                  item={item}
                  readFull={readFull}
                />
              ),
            )}
            {messages.some((m) => m.text_truncated) && (
              <p className="text-xs text-muted">
                {t("Продолжение длинной записи подгрузится ниже.")}
              </p>
            )}
          </section>
        ))}
        {!items.length && (
          <p className="rounded-lg border border-line bg-paper p-4 text-sm text-muted">
            {t("Для этого изменения границы переписки не записаны.")}
          </p>
        )}
        <ErrorNotice>{error}</ErrorNotice>
        {next && (
          <button
            className={buttonStyles.secondary + " w-full"}
            disabled={loading}
            onClick={() => void append()}
          >
            {loading ? t("Загрузка…") : t("Загрузить ещё 20 сообщений")}
          </button>
        )}
      </div>
    </section>
  );
}
