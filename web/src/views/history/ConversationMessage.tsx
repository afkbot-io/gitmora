import { memo } from "react";
import { t } from "../../domain/locale.js";
import {
  toolPresentation,
  type ConversationItem,
  type IndexedMessage,
} from "../../domain/conversation.js";
import { Icon } from "../Icon.js";
import { MessageContent } from "./MessageContent.js";

const RawText = ({ text }: { text: string }) => (
  <pre
    tabIndex={0}
    className="m-0 max-h-96 overflow-auto whitespace-pre-wrap wrap-anywhere rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-6"
  >
    {text || t("(пустое сообщение)")}
  </pre>
);

function ToolEntry({ entry }: { entry: IndexedMessage }) {
  const view = toolPresentation(entry.message);
  return (
    <details
      data-message-index={entry.index + 1}
      className="tool-entry group/tool border-t border-line first:border-0"
    >
      <summary className="flex min-h-11 cursor-pointer items-start gap-3 px-3 py-3 text-sm hover:bg-canvas mobile:flex-wrap">
        <span className="shrink-0 text-muted transition-transform group-open/tool:rotate-90">
          <Icon name="chevron" />
        </span>
        <span className="shrink-0 text-xs text-muted">#{entry.index + 1}</span>
        <span className="shrink-0 font-medium">{view.name}</span>
        <span className="min-w-0 flex-1 wrap-anywhere font-mono text-xs leading-5 text-muted line-clamp-2">
          {view.summary || t("Подробности вызова")}
        </span>
        {view.failed && (
          <span className="text-xs text-danger">{t("Ошибка в записи")}</span>
        )}
      </summary>
      <div className="space-y-3 px-3 pb-3">
        {view.command && (
          <div>
            <p className="mb-2 text-xs font-medium text-muted">
              {t("Команда / входные данные")}
            </p>
            <RawText text={view.command} />
          </div>
        )}
        {view.output && (
          <div>
            <p className="mb-2 text-xs font-medium text-muted">
              {t("Результат")}
            </p>
            <RawText text={view.output} />
          </div>
        )}
        <details>
          <summary className="py-2 text-xs text-muted">
            {t("Исходная запись")}
          </summary>
          <RawText text={entry.message.text} />
        </details>
      </div>
    </details>
  );
}

export const ConversationMessage = memo(function ConversationMessage({
  item,
}: {
  item: ConversationItem;
}) {
  if (item.kind === "tools")
    return (
      <details className="tool-activity min-w-0 rounded-lg border border-line bg-paper">
        <summary className="cursor-pointer px-4 py-3 text-sm text-muted hover:text-ink">
          {t("Действия агента: {0}", item.entries.length)}
          <span className="ml-3 text-xs">
            {[
              ...new Set(
                item.entries.map((e) => e.message.tool_name || "tool"),
              ),
            ]
              .slice(0, 4)
              .join(", ")}
          </span>
        </summary>
        <div>
          {item.entries.map((entry) => (
            <ToolEntry key={entry.index} entry={entry} />
          ))}
        </div>
      </details>
    );
  const { message, index } = item.entry;
  const user = message.role === "user";
  const label = user
    ? t("Вы")
    : message.role === "assistant"
      ? t("Агент")
      : t("Сведения об источнике");
  return (
    <article
      className={`transcript-message min-w-0 ${user ? "rounded-lg border border-line bg-accent-soft p-4 mobile:p-3" : "px-1 py-2"}`}
      data-message-index={index + 1}
    >
      <header className="mb-2 flex items-center justify-between gap-3 text-xs">
        <span className="font-semibold">
          {label}
          {message.kind === "agent_summary" ? ` · ${t("Резюме агента")}` : ""}
        </span>
        <span className="text-muted">#{index + 1}</span>
      </header>
      {message.text.length > 1800 || message.text.split("\n").length > 24 ? (
        <details className="long-message">
          <summary className="cursor-pointer py-1 text-sm text-muted">
            <span className="block whitespace-pre-wrap line-clamp-4">
              {message.text.slice(0, 350)}
            </span>
            <span className="mt-2 block text-accent">
              {t("Развернуть сообщение")}
            </span>
          </summary>
          <div className="mt-3">
            <MessageContent text={message.text} />
          </div>
        </details>
      ) : (
        <MessageContent text={message.text || t("(пустое сообщение)")} />
      )}
    </article>
  );
});
