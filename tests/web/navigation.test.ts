import assert from "node:assert/strict";
import { test } from "node:test";
import { workspaceView } from "../../web/src/domain/navigation.js";

test("workspace locations recognize supported sections and reject arbitrary destinations", () => {
  assert.equal(workspaceView("#view=settings"), "settings");
  assert.equal(workspaceView("#view=setup", "keys"), "setup");
  assert.equal(workspaceView("#view=https://example.invalid"), "context");
  assert.equal(workspaceView("#view=<script>"), "context");
});
test("account-only defaults and CLI consent namespaces remain independent", () => {
  assert.equal(workspaceView("", "keys"), "keys");
  assert.equal(workspaceView("#cli=private-code"), "context");
});
