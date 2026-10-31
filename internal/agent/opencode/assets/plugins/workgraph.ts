import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import type { OpencodeClient } from "@opencode-ai/sdk";
import { Coordinator } from "../workgraph/coordinator.js";
import { LocalCollector } from "../workgraph/collector.js";
import { object } from "../workgraph/normalize.js";

/** OpenCode local plugin. The SDK type import is erased; installation needs no npm package. */
export default async function workgraph(input: {
  client: OpencodeClient;
  directory: string;
  worktree: string;
}) {
  const config = object(
    JSON.parse(
      await readFile(
        fileURLToPath(new URL("../workgraph/config.json", import.meta.url)),
        "utf8",
      ),
    ) as unknown,
  );
  if (config?.version !== 1 || typeof config.executable !== "string")
    throw new Error("Invalid Workgraph installation");
  const coordinator = new Coordinator(
    input.client,
    new LocalCollector(config.executable),
    input.directory,
    () =>
      console.warn(
        "Workgraph: capture needs attention; inspect the local collector",
      ),
    input.worktree,
  );
  await coordinator.resume();
  return {
    "tool.execute.before": async (event: {
      sessionID: string;
      callID: string;
    }) => coordinator.before(event.sessionID, event.callID),
    "tool.execute.after": async (event: {
      sessionID: string;
      callID: string;
    }) => coordinator.after(event.sessionID, event.callID),
    event: async ({ event }: { event: unknown }) => {
      const value = object(event);
      if (
        !value ||
        ![
          "session.idle",
          "session.status",
          "session.compacted",
          "message.updated",
          "message.part.updated",
          "message.removed",
          "message.part.removed",
        ].includes(String(value.type))
      )
        return;
      const props = object(value.properties);
      const sessionID =
        props?.sessionID ??
        object(props?.part)?.sessionID ??
        object(props?.info)?.sessionID;
      if (typeof sessionID === "string") coordinator.schedule(sessionID);
    },
    dispose: async () => coordinator.dispose(),
  };
}
