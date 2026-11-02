import { createHash } from "node:crypto";
import path from "node:path";

export interface Observation {
  schema_version: 1;
  source_session_id: string;
  event_id: string;
  agent: {
    name: string;
    version: string;
    model: string;
    provider?: string;
    model_basis?: string;
  };
  started_at: string;
  messages: Array<{
    role: "user" | "assistant" | "tool";
    text: string;
    tool_name?: string;
    kind?: "agent_summary";
    model?: string;
    provider?: string;
    agent_profile?: string;
    native_id?: string;
    changes?: Array<{ path: string }>;
  }>;
  changed_files: string[];
  file_events: Array<{ id: string; path: string }>;
  native_history?: {
    page_id: string;
    epoch: string;
    message_ids: string[];
    pruned: number[];
    reason: "window" | "compaction";
  };
}

/** Tool positions are adapter-local metadata and never enter the source JSON/digest. */
export interface NativeWindow {
  observation: Observation;
  toolEnds: ReadonlyMap<string, number>;
}

/** Legacy observation-only normalizer used by generic adapter contract fixtures. */
export function normalizePages(
  sessionValue: unknown,
  rowsValue: unknown,
  root: string,
  directory: string,
  sourceRoot = root,
): Observation[] {
  return normalizeWindows(
    sessionValue,
    rowsValue,
    root,
    directory,
    sourceRoot,
  ).map((window) => window.observation);
}

function hash(value: unknown): string {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}
function scopedFile(
  eventID: unknown,
  candidate: unknown,
  directory: string,
  sourceRoot: string,
): Observation["file_events"][number] | undefined {
  if (
    typeof eventID !== "string" ||
    !eventID ||
    eventID.length > 200 ||
    typeof candidate !== "string" ||
    /[\0\r\n\\]/u.test(candidate)
  )
    return undefined;
  const relative = path
    .relative(sourceRoot, path.resolve(directory, candidate))
    .split(path.sep)
    .join("/");
  if (
    !relative ||
    relative === ".git" ||
    relative.startsWith(".git/") ||
    relative === ".." ||
    relative.startsWith("../") ||
    path.isAbsolute(relative)
  )
    return undefined;
  return { id: eventID, path: relative };
}

/** Produces deterministic bounded windows while retaining native item IDs across replay/pruning.
 * Every exposed part remains in a window; an incomplete parallel part still blocks later evidence.
 */
export function normalizeWindows(
  sessionValue: unknown,
  rowsValue: unknown,
  root: string,
  directory: string,
  sourceRoot = root,
): NativeWindow[] {
  const session = object(sessionValue);
  if (!Array.isArray(rowsValue) || rowsValue.length > 50000)
    throw new Error("Native history exceeds row budget");
  // Validate source identity even for an empty available history.
  normalize(sessionValue, [], root, directory, sourceRoot);
  let epoch = hash(["initial", session?.id, object(session?.time)?.created]);
  let reason: "window" | "compaction" = "window";
  let page: Observation | undefined;
  let weight = 0;
  let total = 0;
  let parts = 0;
  let last:
    | {
        id: string;
        message: Observation["messages"][number];
        model: Observation["agent"];
      }
    | undefined;
  const pages: NativeWindow[] = [];
  let toolEnds = new Map<string, number>();
  function finish(): void {
    if (!page?.messages.length) {
      if (page?.file_events.length)
        throw new Error("Native file evidence has no exposed text");
      return;
    }
    page.event_id = hash(page);
    pages.push({ observation: page, toolEnds });
    toolEnds = new Map();
    if (pages.length > 256)
      throw new Error("Native history exceeds window budget");
    page = undefined;
    weight = 0;
    reason = "window";
  }
  function add(
    id: string,
    basis: Observation,
    message?: Observation["messages"][number],
    file?: Observation["file_events"][number],
    pruned = false,
  ): void {
    // Keep the pre-metadata window partition stable across a collector upgrade.
    const legacyMessage = message
      ? {
          role: message.role,
          text: message.text,
          ...(message.tool_name ? { tool_name: message.tool_name } : {}),
          ...(message.kind ? { kind: message.kind } : {}),
        }
      : undefined;
    const size = Buffer.byteLength(JSON.stringify(legacyMessage ?? file)) + 192;
    total += size;
    if (total > 64 * 1024 * 1024)
      throw new Error("Native history exceeds exposed-byte budget");
    if (
      page &&
      (weight + size > 512 * 1024 ||
        page.messages.length >= 1000 ||
        page.file_events.length >= 1000)
    )
      finish();
    if (!page) {
      page = {
        schema_version: 1,
        source_session_id: basis.source_session_id,
        event_id: "",
        agent: basis.agent,
        started_at: basis.started_at,
        messages: [],
        changed_files: [],
        file_events: [],
        native_history: {
          page_id: hash([epoch, id]),
          epoch,
          message_ids: [],
          pruned: [],
          reason,
        },
      };
      if (!message && last) {
        page.messages.push(last.message);
        page.native_history!.message_ids.push(last.id);
        page.agent = last.model;
        weight += Buffer.byteLength(JSON.stringify(last.message)) + 192;
      }
    }
    if (message) {
      if (pruned) page.native_history!.pruned.push(page.messages.length);
      page.messages.push(message);
      page.native_history!.message_ids.push(id);
      page.agent = basis.agent;
      last = { id, message, model: basis.agent };
    }
    if (file) page.file_events.push(file);
    weight += size;
  }
  outer: for (const raw of rowsValue) {
    const row = object(raw);
    const info = object(row?.info);
    if (
      !info ||
      typeof info.id !== "string" ||
      !info.id ||
      !Array.isArray(row?.parts)
    )
      throw new Error("Native window requires stable message identities");
    const completed = typeof object(info.time)?.completed === "number";
    if (info.summary === true && !completed) break;
    if (info.summary === true && completed && !info.error && info.finish) {
      finish();
      epoch = hash(["compaction", info.id]);
      reason = "compaction";
    }
    for (const rawPart of row.parts) {
      if (++parts > 200000)
        throw new Error("Native history exceeds part budget");
      const part = object(rawPart);
      if (!part || typeof part.id !== "string" || !part.id)
        throw new Error("Native window requires stable part identities");
      if (
        part.type === "text" &&
        part.ignored !== true &&
        info.role === "assistant" &&
        !completed &&
        typeof object(part.time)?.end !== "number"
      )
        break outer;
      const state = object(part.state);
      if (
        part.type === "tool" &&
        state?.status !== "completed" &&
        state?.status !== "error"
      )
        break outer;
      let basis = normalize(
        sessionValue,
        [{ info, parts: [part] }],
        root,
        directory,
        sourceRoot,
      );
      if (!basis && part.type === "patch" && Array.isArray(part.files)) {
        const files = part.files
          .map((candidate) =>
            scopedFile(part.id, candidate, directory, sourceRoot),
          )
          .filter(
            (file): file is Observation["file_events"][number] =>
              file !== undefined,
          );
        if (files.length)
          basis = {
            schema_version: 1,
            source_session_id: "opencode:" + String(session?.id),
            event_id: "",
            agent: last?.model ?? {
              name: "opencode",
              version: "workgraph-opencode/4",
              model: "unknown",
            },
            started_at: new Date(
              Number(object(session?.time)?.created),
            ).toISOString(),
            messages: [],
            changed_files: [],
            file_events: files,
          };
      }
      if (!basis) continue;
      basis.agent.version = "workgraph-opencode/4";
      const pruned =
        part.type === "tool" &&
        state?.status === "completed" &&
        (typeof object(state.time)?.compacted === "number" ||
          typeof object(state.time)?.pruned === "number");
      for (const original of basis.messages) {
        const message = { ...original };
        if (message.role === "tool" && basis.file_events.length) {
          message.changes = [
            ...new Set(basis.file_events.map((file) => file.path)),
          ]
            .slice(0, 256)
            .map((path) => ({ path }));
        }
        if (pruned)
          message.text =
            "[Workgraph: source tool output was pruned before this observation]";
        if (info.summary === true && message.role === "assistant")
          message.kind = "agent_summary";
        add(
          hash(["message", info.id, part.id]),
          basis,
          message,
          undefined,
          pruned,
        );
      }
      if (
        part.type === "tool" &&
        typeof part.callID === "string" &&
        part.callID &&
        page?.messages.length
      )
        toolEnds.set(part.callID, page.messages.length);
      for (const file of basis.file_events)
        add(hash(["file", file.id, file.path]), basis, undefined, file);
    }
  }
  finish();
  return pages;
}

/** Reads unknown SDK payloads without importing a provider or executing stored content. */
export function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function text(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}
function clipped(value: string, maximum = 120 * 1024): string {
  const clean = value.replaceAll("\0", "�");
  if (Buffer.byteLength(clean) <= maximum) return clean;
  return (
    Buffer.from(clean)
      .subarray(0, maximum - 64)
      .toString("utf8") + "\n[Workgraph: remaining content omitted due to size]"
  );
}
function canonical(value: unknown, depth = 0): unknown {
  if (depth > 16) return "[Workgraph: nested content omitted]";
  if (Array.isArray(value))
    return value.map((item) => canonical(item, depth + 1));
  const record = object(value);
  if (!record) return value;
  return Object.fromEntries(
    Object.keys(record)
      .sort()
      .map((key) => [key, canonical(record[key], depth + 1)]),
  );
}

/** Extracts only a stable exposed conversation prefix; unfinished parallel parts block later parts. */
export function normalize(
  sessionValue: unknown,
  rowsValue: unknown,
  root: string,
  directory: string,
  sourceRoot = root,
): Observation | undefined {
  const session = object(sessionValue);
  const id = text(session?.id);
  const created = object(session?.time)?.created;
  if (
    !id ||
    id.length > 180 ||
    /[\0\r\n]/u.test(id) ||
    typeof created !== "number" ||
    !Number.isFinite(created)
  )
    throw new Error("Invalid native session");
  const started = new Date(created);
  if (
    !Number.isFinite(started.getTime()) ||
    !Array.isArray(rowsValue) ||
    rowsValue.length > 5000
  )
    throw new Error("Invalid native conversation");
  const messages: Observation["messages"] = [];
  const files: Observation["file_events"] = [];
  const seen = new Set<string>();
  let model = "unknown";
  let modelBasis: "message_metadata" | "thread_configuration" | undefined;
  let partsSeen = 0;
  function file(eventID: unknown, candidate: unknown): void {
    const value = scopedFile(eventID, candidate, directory, sourceRoot);
    if (!value) return;
    const key = value.id + "\0" + value.path;
    if (!seen.has(key)) {
      files.push(value);
      seen.add(key);
    }
  }
  outer: for (const rowValue of rowsValue) {
    const row = object(rowValue);
    const info = object(row?.info);
    if (
      !info ||
      (info.role !== "user" && info.role !== "assistant") ||
      !Array.isArray(row?.parts)
    )
      throw new Error("Invalid native message");
    const configured = object(info.model);
    const modelID = text(info.modelID) ?? text(configured?.modelID);
    const providerID = text(info.providerID) ?? text(configured?.providerID);
    const profile = text(info.agent);
    if (modelID) {
      model = providerID ? providerID + "/" + modelID : modelID;
      modelBasis =
        info.role === "assistant" ? "message_metadata" : "thread_configuration";
    }
    for (const value of row.parts) {
      if (++partsSeen > 15000)
        throw new Error("Native conversation exceeds part limit");
      const part = object(value);
      if (!part) throw new Error("Invalid native part");
      if (part.type === "text") {
        if (part.ignored === true) continue;
        if (
          info.role === "assistant" &&
          typeof object(info.time)?.completed !== "number" &&
          typeof object(part.time)?.end !== "number"
        )
          break outer;
        if (typeof part.text !== "string")
          throw new Error("Invalid native text");
        messages.push({
          role: info.role,
          text: clipped(part.text),
          ...(typeof part.id === "string" ? { native_id: part.id } : {}),
          ...(info.role === "assistant"
            ? {
                ...(modelID ? { model: modelID } : {}),
                ...(providerID ? { provider: providerID } : {}),
                ...(profile ? { agent_profile: profile } : {}),
              }
            : {}),
        });
      } else if (part.type === "tool") {
        const state = object(part.state);
        if (
          !state ||
          (state.status !== "completed" && state.status !== "error")
        )
          break outer;
        const tool = text(part.tool);
        if (!tool) throw new Error("Invalid native tool");
        const result =
          state.status === "completed" ? text(state.output) : text(state.error);
        if (result === undefined) throw new Error("Invalid native tool result");
        messages.push({
          role: "tool",
          tool_name: tool,
          ...(typeof part.id === "string" ? { native_id: part.id } : {}),
          ...(modelID ? { model: modelID } : {}),
          ...(providerID ? { provider: providerID } : {}),
          ...(profile ? { agent_profile: profile } : {}),
          text: clipped(
            "Input:\n" +
              clipped(JSON.stringify(canonical(state.input ?? {})), 48 * 1024) +
              "\nResult:\n" +
              clipped(result, 72 * 1024),
          ),
        });
        if (state.status === "completed") {
          const metadata = object(state.metadata);
          if (tool === "write" || tool === "edit")
            file(part.id, metadata?.filepath ?? object(state.input)?.filePath);
          if (tool === "apply_patch" && Array.isArray(metadata?.files)) {
            for (const item of metadata.files) {
              const changed = object(item);
              file(part.id, changed?.filePath);
              file(part.id, changed?.movePath);
            }
          }
        }
      } else if (part.type === "patch" && Array.isArray(part.files)) {
        for (const candidate of part.files) file(part.id, candidate);
      }
      // Reasoning, data URLs and internal snapshot/step parts are intentionally outside capture.
    }
  }
  if (messages.length === 0) return undefined;
  const observation: Observation = {
    schema_version: 1,
    source_session_id: "opencode:" + id,
    event_id: "",
    agent: {
      name: "opencode",
      version: "workgraph-opencode/4",
      model,
      ...(modelBasis ? { model_basis: modelBasis } : {}),
    },
    started_at: started.toISOString(),
    messages,
    changed_files: [],
    file_events: files,
  };
  const bytes = JSON.stringify(observation);
  if (
    messages.length > 5000 ||
    files.length > 5000 ||
    Buffer.byteLength(bytes) > 2 * 1024 * 1024
  )
    throw new Error("Native observation exceeds snapshot limit");
  observation.event_id = createHash("sha256").update(bytes).digest("hex");
  return observation;
}
