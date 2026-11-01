import type {
  CaptureWindow,
  SessionRevision,
  MessageCursor,
} from "./history.js";

export type Message = SessionRevision["snapshot"]["messages"][number];
export interface IndexedMessage {
  preview?: boolean;
  fullTextAfter?: MessageCursor;
  message: Message;
  index: number;
}
export type ConversationItem =
  | { kind: "message"; key: number; entry: IndexedMessage }
  | { kind: "tools"; key: number; entries: IndexedMessage[] };

/** Filtering keeps original source positions; grouping never reorders tool activity. */
export function conversationItems(
  messages: Message[],
  visible: number,
  dialogueOnly: boolean,
  query: string,
): ConversationItem[] {
  const result: ConversationItem[] = [];
  const term = query.trim().toLocaleLowerCase();
  messages.slice(0, visible).forEach((message, index) => {
    if (dialogueOnly && message.role !== "user" && message.role !== "assistant")
      return;
    if (
      term &&
      !(message.text + " " + (message.tool_name ?? ""))
        .toLocaleLowerCase()
        .includes(term)
    )
      return;
    const entry = { message, index };
    const previous = result.at(-1);
    if (message.role === "tool") {
      if (previous?.kind === "tools") previous.entries.push(entry);
      else result.push({ kind: "tools", key: index, entries: [entry] });
    } else result.push({ kind: "message", key: index, entry });
  });
  return result;
}

type RecordValue = Record<string, unknown>;
function record(value: unknown): RecordValue | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as RecordValue)
    : null;
}
function objectText(value: unknown): RecordValue | null {
  if (typeof value !== "string") return record(value);
  if (value.length > 262144) return null;
  try {
    return record(JSON.parse(value));
  } catch {
    return null;
  }
}
function string(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** Display only supplied fields. A recorded status is not independent execution verification. */
export function toolPresentation(message: Message): {
  name: string;
  summary: string;
  command: string;
  output: string;
  failed: boolean;
} {
  const value = objectText(message.text);
  const input =
    objectText(value?.arguments) ?? objectText(value?.input) ?? value;
  const command =
    string(input?.cmd) || string(input?.command) || string(input?.code);
  const path =
    string(input?.path) ||
    string(input?.file_path) ||
    string(input?.url) ||
    string(input?.q);
  const output =
    string(value?.output) || string(value?.stdout) || string(value?.result);
  return {
    name: message.tool_name || "tool",
    summary: (
      command ||
      path ||
      (value ? string(value.description) : message.text)
    )
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, 180),
    command,
    output,
    failed:
      typeof value?.exit_code === "number"
        ? value.exit_code !== 0
        : value?.isError === true || value?.status === "failed",
  };
}

export interface ConversationGroup {
  key: string;
  first: CaptureWindow;
  windows: CaptureWindow[];
}
/** Only server-proven chains may collapse into one navigable conversation. */
export function groupConversations(
  windows: CaptureWindow[],
): ConversationGroup[] {
  const groups = new Map<string, ConversationGroup>();
  for (const window of windows) {
    const root = window.chain_verified
      ? window.root_session_id
      : window.source.session_id;
    const key = `${window.source.repository_id}/${root}`;
    const existing = groups.get(key);
    if (existing) {
      if (
        !existing.windows.some(
          (w) => w.source.session_id === window.source.session_id,
        )
      )
        existing.windows.push(window);
      if (window.segment < existing.first.segment) existing.first = window;
    } else groups.set(key, { key, first: window, windows: [window] });
  }
  return [...groups.values()].map((g) => ({
    ...g,
    windows: g.windows.sort((a, b) => a.segment - b.segment),
  }));
}

/** A view location never enters the API's authorization selector. */
export function transcriptLocation(hash: string): {
  revision: number;
  message: number | null;
} {
  const p = new URLSearchParams(hash.replace(/^#/, ""));
  const revision = Number(p.get("revision"));
  const position = p.get("message");
  const message = position === null ? null : Number(position);
  return {
    revision:
      Number.isSafeInteger(revision) &&
      revision > 0 &&
      revision <= 1_000_000_000
        ? revision
        : 0,
    message:
      message !== null &&
      Number.isSafeInteger(message) &&
      message >= 0 &&
      message < 1_000_000
        ? message
        : null,
  };
}
