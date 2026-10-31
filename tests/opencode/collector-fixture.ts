import type {
  Collector,
  Frontier,
  Probe,
} from "../../internal/agent/opencode/assets/workgraph/collector.js";
import type { Observation } from "../../internal/agent/opencode/assets/workgraph/normalize.js";

/** Durable-port fixture shared across coordinator instances; observations remain independent spies. */
export function collectorFixture(
  ports: {
    probe(directory: string): Promise<Probe | undefined>;
    record(
      directory: string,
      observation: Observation,
      before?: string,
      deliver?: boolean,
    ): Promise<boolean>;
  },
  journal = new Map<string, Frontier>(),
): Collector {
  return {
    probe: ports.probe,
    begin: async (directory, session, call) => {
      const key = JSON.stringify([session, call]);
      if (journal.has(key)) return;
      const probe = await ports.probe(directory);
      if (!probe) throw new Error("Uninitialized");
      journal.set(key, {
        id: (journal.size + 1).toString(16).padStart(64, "0"),
        sourceSessionID: session,
        before: probe.head,
        after: null,
        targetSessionID: "",
      });
    },
    end: async (directory, session, call) => {
      const entry = journal.get(JSON.stringify([session, call]));
      const probe = await ports.probe(directory);
      if (!entry || !probe) throw new Error("No initial boundary");
      if (entry.after === null)
        journal.set(JSON.stringify([session, call]), {
          ...entry,
          after: probe.head,
        });
    },
    frontiers: async () => [...journal.values()],
    record: async (directory, observation, ids, deliver) => {
      const selected = [...journal.entries()].filter(([, value]) =>
        ids?.includes(value.id),
      );
      const ok = await ports.record(
        directory,
        observation,
        selected[0]?.[1].before,
        deliver,
      );
      if (ok)
        for (const [key, value] of selected)
          journal.set(key, {
            ...value,
            targetSessionID: "22222222-2222-4222-8222-222222222222",
          });
      return ok;
    },
    apply: async (_directory, id) => {
      const selected = [...journal.entries()].find(
        ([, value]) => value.id === id,
      );
      if (
        !selected ||
        !selected[1].targetSessionID ||
        selected[1].after === null
      )
        throw new Error("Unbound");
      journal.delete(selected[0]);
    },
  };
}
