import assert from "node:assert/strict";
import test from "node:test";
import {
  clientInstallCommand,
  onboardingCommands,
} from "../../web/src/domain/onboarding.js";

test("onboarding uses one fixed server and contains no embedded credentials", () => {
  const commands = onboardingCommands(
    "https://gitmora.tasktopia.online",
    "11111111-1111-4111-8111-111111111111",
  );
  assert.equal(
    commands.login,
    "gitmora auth login --server 'https://gitmora.tasktopia.online'",
  );
  assert.match(
    commands.init,
    /--repository-id 11111111-1111-4111-8111-111111111111$/,
  );
  assert.deepEqual(JSON.parse(commands.opencodeMCP).mcp.gitmora.command, [
    "gitmora",
    "mcp",
    "--server",
    "https://gitmora.tasktopia.online",
  ]);
  assert.ok(!JSON.stringify(commands).includes("WORKGRAPH_TOKEN"));
});

test("generated commands reject injected IDs, credentials and alternate URL destinations", () => {
  const id = "11111111-1111-4111-8111-111111111111";
  for (const url of [
    "https://user:secret@example.com",
    "https://example.com/path",
    "https://example.com?x=1",
    "http://example.com",
    "file:///tmp/socket",
  ]) {
    assert.throws(() => onboardingCommands(url, id));
  }
  assert.throws(() =>
    onboardingCommands("https://example.com", id + "; touch injected"),
  );
  assert.ok(onboardingCommands("http://127.0.0.1:8080", id));
});

test("account setup is available before any repository is assigned", () => {
  const commands = onboardingCommands("https://gitmora.tasktopia.online");
  assert.equal(commands.init, "");
  assert.match(commands.login, /auth login/);
  assert.match(commands.codexMCP, /mcp_servers.gitmora/);
  assert.throws(() => onboardingCommands("https://example.com", ""));
});

test("platform-specific installation requires exact named checksums before executing downloaded bytes", () => {
  const mac = clientInstallCommand(
    "https://gitmora.tasktopia.online",
    "gitmora_darwin_arm64",
  );
  assert.match(mac, /shasum -a 256 -c selected.sha256/);
  assert.ok(mac.indexOf("-c selected.sha256") < mac.indexOf("chmod 700"));
  assert.match(
    clientInstallCommand("https://example.com", "gitmora_linux_amd64"),
    /sha256sum -c/,
  );
  assert.throws(() =>
    clientInstallCommand(
      "https://bad:secret@example.com",
      "gitmora_linux_amd64",
    ),
  );
});
