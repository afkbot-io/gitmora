import { normalizeWindows, object } from "./normalize.js";
import {
  frontierID,
  type CheckpointBinding,
  type Collector,
} from "./collector.js";
import {
  readHistory,
  type HistoryQuery,
  type HistoryReply,
} from "./history.js";

interface NativeClient {
  session: {
    get(query: HistoryQuery): Promise<HistoryReply>;
    messages(query: HistoryQuery): Promise<HistoryReply>;
  };
}

/** Serializes lifecycle observations and re-reads the latest SDK state when work actually runs. */
export class Coordinator {
  private queue: Promise<void> = Promise.resolve();
  private readonly pending = new Set<string>();
  private readonly active = new Set<string>();
  private closed = false;
  constructor(
    private readonly client: NativeClient,
    private readonly collector: Collector,
    private readonly directory: string,
    private readonly report: () => void,
    private readonly sourceRoot?: string,
  ) {}

  private enqueue<T>(work: () => Promise<T>): Promise<T | undefined> {
    const next = this.queue.then(work).catch(() => {
      this.report();
      return undefined;
    });
    this.queue = next.then(() => {});
    return next;
  }
  private async capture(sessionID: string): Promise<string | undefined> {
    const probe = await this.collector.probe(this.directory);
    if (!probe) return undefined; // Global installation is inactive outside explicitly initialized repositories.
    for (const entry of await this.collector.frontiers(this.directory)) {
      if (entry.targetSessionID) {
        try {
          await this.collector.apply(this.directory, entry.id);
        } catch {
          this.report();
        }
      }
    }
    const query = {
      path: { id: sessionID },
      query: { directory: this.directory },
      signal: AbortSignal.timeout(3000),
    };
    const session = await this.client.session.get(query);
    if (session.error !== undefined || object(session.data)?.id !== sessionID)
      throw new Error("Native session identity changed");
    const waiting = await this.collector.frontiers(this.directory);
    const selected = waiting.filter(
      (entry) =>
        entry.sourceSessionID === sessionID &&
        entry.after !== null &&
        !entry.targetSessionID,
    );
    const messages = await readHistory(
      this.client.session,
      sessionID,
      this.directory,
    );
    const pages = normalizeWindows(
      session.data,
      messages,
      probe.root,
      this.directory,
      this.sourceRoot,
    );
    if (!pages.length) return probe.head;
    const matched = new Set<string>();
    for (const [i, window] of pages.entries()) {
      const bindings: CheckpointBinding[] = [];
      for (const [callID, messageEnd] of window.toolEnds) {
        const entry = selected.find(
          (entry) => entry.id === frontierID(entry.nativeKey, callID),
        );
        if (entry) {
          bindings.push({ id: entry.id, messageEnd });
          matched.add(entry.id);
        }
      }
      bindings.sort((a, b) => a.messageEnd - b.messageEnd);
      if (
        !(await this.collector.record(
          this.directory,
          window.observation,
          bindings,
          i === pages.length - 1,
        ))
      )
        throw new Error("Collector rejected observation");
      for (const binding of bindings)
        await this.collector.apply(this.directory, binding.id);
    }
    if (selected.some((entry) => !matched.has(entry.id))) this.report();
    this.active.add(sessionID);
    return probe.head;
  }
  schedule(sessionID: string): void {
    if (this.closed || this.pending.has(sessionID)) return;
    this.pending.add(sessionID);
    void this.enqueue(async () => {
      this.pending.delete(sessionID);
      await this.capture(sessionID);
    });
  }
  async before(sessionID: string, callID: string): Promise<void> {
    if (this.closed) return;
    await this.enqueue(async () => {
      if (!(await this.collector.probe(this.directory))) return;
      await this.collector.begin(this.directory, sessionID, callID);
      this.active.add(sessionID);
      await this.capture(sessionID);
    });
  }
  async after(sessionID: string, callID: string): Promise<void> {
    if (this.closed) return;
    // Persist the observed end before waiting behind any queued SDK/history request.
    try {
      if (!(await this.collector.probe(this.directory))) return;
      await this.collector.end(this.directory, sessionID, callID);
    } catch {
      this.report();
      return;
    }
    await this.enqueue(() => this.capture(sessionID));
  }
  /** Applies bound intents without SDK access and recaptures only completed unbound sources. */
  async resume(): Promise<void> {
    if (this.closed) return;
    await this.enqueue(async () => {
      if (!(await this.collector.probe(this.directory))) return;
      const pending = await this.collector.frontiers(this.directory);
      for (const entry of pending)
        if (entry.targetSessionID) {
          try {
            await this.collector.apply(this.directory, entry.id);
          } catch {
            this.report();
          }
        }
      const sources = new Set(
        pending
          .filter((entry) => entry.after !== null && !entry.targetSessionID)
          .map((entry) => entry.sourceSessionID),
      );
      for (const source of sources) {
        try {
          await this.capture(source);
        } catch {
          this.report();
        }
      }
    });
  }
  async dispose(): Promise<void> {
    this.closed = true;
    await this.queue;
    for (const session of this.active)
      await this.enqueue(() => this.capture(session));
  }
}
