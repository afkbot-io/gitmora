import assert from "node:assert/strict";
import test from "node:test";
import {
  cliCode,
  cliCodeFromHash,
  cliLoginPath,
  checkCLIView,
  cliDecision,
} from "../../web/src/domain/cli.js";
import type { CLIView } from "../../web/src/domain/cli.js";
const id = "11111111-1111-4111-8111-111111111111";
const user = "22222222-2222-4222-8222-222222222222";
const view: CLIView = {
  id,
  label: "Developer laptop",
  scopes: ["capture:write", "credentials:manage"],
  ttl_seconds: 86400,
  created_at: "2026-10-30T12:00:00Z",
  expires_at: "2026-10-30T12:10:00Z",
  fingerprint: "a".repeat(64),
  status: "pending",
};
test("CLI browser entry accepts only a complete public code and fixed same-application login path", () => {
  assert.equal(cliCode("abcd-efgh"), "ABCD-EFGH");
  assert.equal(cliCodeFromHash("#cli=ABCD-EFGH"), "ABCD-EFGH");
  assert.equal(cliCodeFromHash("#login=failed"), "");
  assert.equal(cliCodeFromHash("#cli=ABCD-EFGH&return_to=external"), "");
  assert.equal(cliLoginPath("abcd-efgh"), "/v1/auth/cli/login?code=ABCD-EFGH");
  for (const bad of [
    "0000-0000",
    "abcd/efgh",
    "https://evil.invalid",
    "ABCD-EFGHI",
    "ABCD\nEFGH",
  ])
    assert.throws(() => cliCode(bad));
});
test("CLI consent pins complete inspected metadata and cannot confirm a different owner or administrative grant", () => {
  checkCLIView(view, user);
  assert.deepEqual(cliDecision("ABCD-EFGH", view, user, true), {
    user_code: "ABCD-EFGH",
    expected_fingerprint: view.fingerprint,
    approve: true,
  });
  for (const bad of [
    { ...view, scopes: ["platform:manage", "credentials:manage"] },
    { ...view, ttl_seconds: 90 * 86400 + 1 },
    { ...view, fingerprint: "x" },
    { ...view, expires_at: "invalid" },
    {
      ...view,
      status: "approved",
      user_id: id,
      token_id: user,
      credential_expires_at: "2026-11-01T12:00:00Z",
    },
  ])
    assert.throws(() => checkCLIView(bad as CLIView, user));
  assert.throws(() =>
    cliDecision(
      "ABCD-EFGH",
      { ...view, status: "denied", user_id: user },
      user,
      true,
    ),
  );
});
test("device-cancelled enrollment has no account or key and cannot be approved", () => {
  const cancelled = { ...view, status: "cancelled" } as CLIView;
  checkCLIView(cancelled, user);
  assert.throws(() => cliDecision("ABCD-EFGH", cancelled, user, true));
  assert.throws(() => checkCLIView({ ...cancelled, user_id: user }, user));
  assert.throws(() => checkCLIView({ ...cancelled, token_id: id }, user));
});
