import assert from "node:assert/strict";
import { mkdtemp, writeFile, chmod, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LocalCollector } from "../../internal/agent/opencode/assets/workgraph/collector.js";

test("local probe denies disabled repository or agent before source acquisition", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "gitmora-policy-"));
  try {
    const executable = path.join(directory, "collector");
    for (const [policy, allowed] of [
      [{ collection_enabled: false }, false],
      [{ collection_enabled: true, disabled_agents: ["opencode"] }, false],
      [{ collection_enabled: true, disabled_agents: ["codex"] }, true],
      [{}, true],
    ] as const) {
      const response = JSON.stringify({
        worktree_root: directory,
        head: "",
        ...policy,
      });
      await writeFile(
        executable,
        `#!/usr/bin/env node\nprocess.stdout.write(${JSON.stringify(response)});\n`,
      );
      await chmod(executable, 0o700);
      assert.equal(
        Boolean(await new LocalCollector(executable).probe(directory)),
        allowed,
      );
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("native probe explicitly selects physical worktree handling in the collector", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "gitmora-physical-"));
  try {
    const executable = path.join(directory, "collector");
    const response = JSON.stringify({
      worktree_root: directory,
      head: "",
      collection_enabled: true,
    });
    await writeFile(
      executable,
      `#!/usr/bin/env node\nif (JSON.stringify(process.argv.slice(2)) !== JSON.stringify(["status","--physical-worktree"])) process.exit(1);\nprocess.stdout.write(${JSON.stringify(response)});\n`,
    );
    await chmod(executable, 0o700);
    assert.equal(
      (await new LocalCollector(executable).probe(directory))?.root,
      directory,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
