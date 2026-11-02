import type {
  ContextPack,
  MessageCursor,
  ScopedMessage,
  Selection,
  SessionRevision,
} from "../domain/history.js";
import { packQuery, sameSelection } from "../domain/history.js";
import { messageBatchSize } from "../domain/conversation.js";

export { messageBatchSize };
const mismatch = () => new Error("Контекст не совпал с выбранным источником.");

/** Fill one visible batch across byte-bounded API pages, without requesting surplus records. */
export async function messageBatch(
  selection: Selection,
  read: (query: Selection) => Promise<ContextPack>,
  after?: MessageCursor,
  initial?: ContextPack,
): Promise<{ items: ScopedMessage[]; next?: MessageCursor }> {
  const items: ScopedMessage[] = [];
  const cursors = new Set<string>();
  const records = new Set<string>();
  let next = after;
  for (let page = 0; page < messageBatchSize; page++) {
    const query = packQuery({
      ...selection,
      message_view: "compact",
      message_limit: messageBatchSize - items.length,
      ...(next ? { message_after: next } : {}),
    });
    const result = page === 0 && initial ? initial : await read(query);
    if (!sameSelection(query, result.query) || !result.transcript)
      throw mismatch();
    const received = result.transcript.items;
    const repository = selection.merge_request_iid
      ? result.merge_requests.items[0]?.source_repository_id
      : selection.repository_id;
    if (received.length > messageBatchSize - items.length) throw mismatch();
    for (const item of received) {
      const key = `${item.source.repository_id}/${item.source.session_id}/${item.index}`;
      if (
        records.has(key) ||
        item.source.project_id !== selection.project_id ||
        (repository && item.source.repository_id !== repository) ||
        item.text_truncated ||
        (item.message_offset ?? 0) !== 0
      )
        throw mismatch();
      records.add(key);
      items.push(item);
    }
    next = result.transcript.next;
    if (!next) {
      if (result.transcript.has_more) throw mismatch();
      return { items };
    }
    const key = JSON.stringify(next);
    if (cursors.has(key) || !received.length) throw mismatch();
    cursors.add(key);
    if (items.length === messageBatchSize) return { items, next };
  }
  throw mismatch();
}

/** Previous history is an explicit session read, independent of a commit/MR selection. */
export async function previousMessageBatch(
  project: string,
  before: MessageCursor,
  read: (cursor: MessageCursor) => Promise<SessionRevision>,
): Promise<{ items: ScopedMessage[]; before?: MessageCursor }> {
  const items: ScopedMessage[] = [];
  let cursor = before;
  let fromEnd = false;
  const visited = new Set<string>();
  let oldest = before;
  for (let window = 0; window <= messageBatchSize; window++) {
    if (visited.has(cursor.session_id)) throw mismatch();
    visited.add(cursor.session_id);
    const value = await read(cursor);
    const snapshot = value.snapshot;
    const segment = snapshot.continuation?.segment ?? 1;
    if (
      snapshot.repository_id !== cursor.repository_id ||
      snapshot.session_id !== cursor.session_id ||
      snapshot.revision !== cursor.revision ||
      value.digest !== cursor.digest ||
      segment !== cursor.segment
    )
      throw mismatch();
    const end = fromEnd ? snapshot.messages.length : cursor.offset;
    if (end < 0 || end > snapshot.messages.length) throw mismatch();
    const start = Math.max(0, end - (messageBatchSize - items.length));
    oldest = { ...cursor, offset: start };
    items.unshift(
      ...snapshot.messages.slice(start, end).map((message, index) => ({
        source: {
          kind: "capture" as const,
          project_id: project,
          repository_id: cursor.repository_id,
          session_id: cursor.session_id,
          revision: cursor.revision,
          digest: cursor.digest,
        },
        segment,
        index: start + index,
        message,
      })),
    );
    const previous = snapshot.continuation;
    const verified = value.parent_reference_verified && previous && segment > 1;
    if (start > 0 || items.length === messageBatchSize) {
      return {
        items,
        ...(start > 0 || verified
          ? { before: { ...cursor, offset: start } }
          : {}),
      };
    }
    if (!verified) return { items };
    cursor = {
      repository_id: cursor.repository_id,
      session_id: previous.previous_session_id,
      revision: previous.previous_revision,
      digest: previous.previous_digest,
      segment: segment - 1,
      offset: 0,
    };
    fromEnd = true;
  }
  return { items, before: oldest };
}
