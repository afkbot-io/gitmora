import { execFile } from "node:child_process";
import path from "node:path";
import { object, type Observation } from "./normalize.js";

export interface Probe {
  root: string;
  head: string;
}
/** Private source locator and immutable Git boundaries returned by the durable local CLI. */
export interface Frontier {
  readonly id: string;
  readonly sourceSessionID: string;
  readonly before: string;
  readonly after: string | null;
  readonly targetSessionID: string;
}
export interface Collector {
  probe(directory: string): Promise<Probe | undefined>;
  begin(directory: string, sessionID: string, callID: string): Promise<void>;
  end(directory: string, sessionID: string, callID: string): Promise<void>;
  frontiers(directory: string): Promise<readonly Frontier[]>;
  apply(directory: string, id: string): Promise<void>;
  record(
    directory: string,
    observation: Observation,
    frontiers?: readonly string[],
    deliver?: boolean,
  ): Promise<boolean>;
}

const sha = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/u;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u;
function head(value: unknown): value is string {
  return typeof value === "string" && (value === "" || sha.test(value));
}
function frontier(value: unknown): Frontier {
  const entry = object(value),
    source = object(entry?.source);
  if (
    typeof entry?.id !== "string" ||
    !/^[0-9a-f]{64}$/u.test(entry.id) ||
    source?.agent !== "opencode" ||
    typeof source.source_session_id !== "string" ||
    !source.source_session_id.startsWith("opencode:") ||
    source.source_session_id.length <= 9 ||
    source.source_session_id.length > 200 ||
    !head(entry.before) ||
    !(entry.after === null || head(entry.after)) ||
    typeof entry.target_session_id !== "string" ||
    (entry.target_session_id !== "" &&
      (!uuid.test(entry.target_session_id) || entry.after === null)) ||
    entry.applied !== false ||
    entry.abandoned !== false
  )
    throw new Error("Invalid local frontier metadata");
  return {
    id: entry.id,
    sourceSessionID: source.source_session_id.slice(9),
    before: entry.before,
    after: entry.after,
    targetSessionID: entry.target_session_id,
  };
}

/** Runs the selected absolute binary with bounded stdin JSON and no shell interpolation. */
export class LocalCollector implements Collector {
  constructor(private readonly executable: string) {
    if (!path.isAbsolute(executable))
      throw new Error("Collector executable must be absolute");
  }
  private execute(
    directory: string,
    args: string[],
    input?: unknown,
  ): Promise<unknown> {
    return new Promise((resolve) => {
      const child = execFile(
        this.executable,
        [...args, "--physical-worktree"],
        {
          cwd: directory,
          timeout: 6000,
          maxBuffer: 256 * 1024,
          encoding: "utf8",
          windowsHide: true,
        },
        (error, stdout) => {
          if (error) {
            resolve(undefined);
            return;
          }
          try {
            resolve(JSON.parse(stdout) as unknown);
          } catch {
            resolve(undefined);
          }
        },
      );
      child.stdin?.on("error", () => {});
      child.stdin?.end(input === undefined ? undefined : JSON.stringify(input));
    });
  }
  async probe(directory: string): Promise<Probe | undefined> {
    const reply = object(await this.execute(directory, ["status"]));
    if (
      reply?.collection_enabled === false ||
      (Array.isArray(reply?.disabled_agents) &&
        reply.disabled_agents.includes("opencode"))
    )
      return undefined;
    if (typeof reply?.worktree_root !== "string" || !head(reply.head))
      return undefined;
    return { root: reply.worktree_root, head: reply.head };
  }
  private async boundary(
    directory: string,
    sessionID: string,
    callID: string,
    command: "begin" | "end",
  ): Promise<void> {
    const reply = object(
      await this.execute(directory, ["frontier", command], {
        agent: "opencode",
        source_session_id: "opencode:" + sessionID,
        call_id: callID,
      }),
    );
    if (
      typeof reply?.id !== "string" ||
      !/^[0-9a-f]{64}$/u.test(reply.id) ||
      object(reply.source)?.agent !== "opencode" ||
      object(reply.source)?.source_session_id !== "opencode:" + sessionID ||
      !head(reply.before) ||
      !(reply.after === null || head(reply.after)) ||
      (command === "end" && reply.after === null)
    )
      throw new Error("Local frontier was not persisted");
  }
  async begin(
    directory: string,
    sessionID: string,
    callID: string,
  ): Promise<void> {
    await this.boundary(directory, sessionID, callID, "begin");
  }
  async end(
    directory: string,
    sessionID: string,
    callID: string,
  ): Promise<void> {
    await this.boundary(directory, sessionID, callID, "end");
  }
  async frontiers(directory: string): Promise<readonly Frontier[]> {
    const entries = object(
      await this.execute(directory, ["frontier", "list"]),
    )?.frontiers;
    if (!Array.isArray(entries) || entries.length > 64)
      throw new Error("Local frontier journal unavailable");
    // Other adapters share the ledger; retain their pending entries without reading their sources.
    const result = entries
      .filter((entry) => object(object(entry)?.source)?.agent === "opencode")
      .map(frontier);
    if (new Set(result.map((entry) => entry.id)).size !== result.length)
      throw new Error("Duplicate local frontier");
    return result;
  }
  async apply(directory: string, id: string): Promise<void> {
    const reply = object(
      await this.execute(directory, [
        "frontier",
        "apply",
        "--id",
        id,
        "--sync",
      ]),
    );
    if (reply?.id !== id || reply.status !== "applied")
      throw new Error("Local frontier remains pending");
  }
  async record(
    directory: string,
    observation: Observation,
    frontiers: readonly string[] = [],
    deliver = true,
  ): Promise<boolean> {
    const args = ["capture", "--pin-source"];
    if (deliver) args.push("--sync");
    for (const id of frontiers) args.push("--frontier", id);
    return (
      object(await this.execute(directory, args, observation))?.status ===
      "recorded"
    );
  }
}
