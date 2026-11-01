import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../domain/locale.js";
import {
  packQuery,
  sameSelection,
  type ContextPack,
  type Selection,
  type ScopedMessage,
} from "../../domain/history.js";
import type { ConversationItem } from "../../domain/conversation.js";
import { readableError } from "../../domain/context.js";
import type { ApiClient } from "../../transport/api.js";
import { ConversationMessage } from "./ConversationMessage.js";
import { ErrorNotice } from "../shared.js";
import { buttonStyles } from "../ui.js";

function group(
  items: ScopedMessage[],
  onlyDialogue: boolean,
  query: string,
): ConversationItem[] {
  const result: ConversationItem[] = [];
  for (const item of items) {
    if (onlyDialogue && !["user", "assistant"].includes(item.message.role))
      continue;
    if (
      query &&
      !item.message.text.toLocaleLowerCase().includes(query.toLocaleLowerCase())
    )
      continue;
    const entry = { message: item.message, index: item.index };
    const previous = result.at(-1);
    if (item.message.role === "tool") {
      if (previous?.kind === "tools") previous.entries.push(entry);
      else result.push({ kind: "tools", key: item.index, entries: [entry] });
    } else result.push({ kind: "message", key: item.index, entry });
  }
  return result;
}

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
  const mounted = useRef(true);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    if (anchor.current !== null) {
      window.scrollTo({ top: anchor.current, behavior: "instant" });
      anchor.current = null;
    }
  }, [items]);
  async function append() {
    if (!next || loading) return;
    setLoading(true);
    setError("");
    try {
      const result = await client.request<ContextPack>(
        "/v1/context/pack",
        packQuery({ ...selection, message_after: next }),
      );
      if (!mounted.current) return;
      if (!sameSelection(selection, result.query))
        throw new Error("Контекст не совпал с выбранным источником.");
      anchor.current = window.scrollY;
      setItems((current) => {
        const merged = [...current];
        for (const item of result.transcript?.items ?? []) {
          const index = merged.findIndex(
            (old) =>
              old.source.session_id === item.source.session_id &&
              old.index === item.index,
          );
          if (index < 0) merged.push(item);
          else if (
            (item.message_offset ?? 0) > 0 &&
            Array.from(merged[index]!.message.text).length ===
              item.message_offset
          )
            merged[index] = {
              ...item,
              message_offset: 0,
              message: {
                ...item.message,
                text: merged[index]!.message.text + item.message.text,
              },
            };
        }
        return merged;
      });
      setNext(result.transcript?.next);
    } catch (e) {
      if (mounted.current) setError(readableError(e));
    } finally {
      if (mounted.current) setLoading(false);
    }
  }
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
      <div className="mx-auto grid max-w-240 gap-4">
        {[...parts].map(([id, messages]) => (
          <section key={id} className="min-w-0 space-y-4">
            {group(messages, onlyDialogue, query).map((item) => (
              <ConversationMessage key={item.key} item={item} />
            ))}
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
            {loading ? t("Загрузка…") : t("Загрузить ещё сообщения")}
          </button>
        )}
      </div>
    </section>
  );
}
