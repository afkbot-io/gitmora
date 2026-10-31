import assert from "node:assert/strict";
import test from "node:test";
import {
  keyInput,
  keyState,
  loginNotice,
  maximumLifetime,
} from "../../web/src/domain/credentials.js";

const parent = {
  token_id: "parent",
  scopes: ["capture:read", "credentials:manage"],
  expires_at: "2026-10-30T11:00:00Z",
};
const now = Date.parse("2026-10-30T10:00:00Z");
test("delegation is explicit, bounded by the remaining parent lifetime and never silently reduced", () => {
  assert.equal(maximumLifetime(parent.expires_at, now), 3600);
  assert.deepEqual(
    keyInput(
      parent,
      " Local collector ",
      ["capture:read", "capture:read"],
      "30",
      now,
    ),
    { label: "Local collector", scopes: ["capture:read"], ttl_seconds: 1800 },
  );
  assert.throws(
    () => keyInput(parent, "Key", ["capture:write"], "30", now),
    /прав/,
  );
  assert.throws(() => keyInput(parent, "Key", [], "30", now), /разрешение/);
  assert.throws(
    () => keyInput(parent, "Key", ["capture:read"], "61", now),
    /срок/,
  );
  for (const value of ["0", "1.2", "1e2", "-1"])
    assert.throws(() => keyInput(parent, "Key", ["capture:read"], value, now));
  assert.equal(maximumLifetime("2026-10-30T09:00:00Z", now), 0);
});

test("key labels are bounded UTF-8 and active/expired/revoked states do not promise reusable browser credentials", () => {
  assert.throws(
    () => keyInput(parent, "я".repeat(61), ["capture:read"], "1", now),
    /название/,
  );
  const key = { expires_at: parent.expires_at };
  assert.equal(keyState(key, now), "active");
  assert.equal(keyState(key, now + 3600000), "expired");
  assert.equal(
    keyState({ ...key, revoked_at: "2026-10-30T09:00:00Z" }, now),
    "revoked",
  );
});

test("login recovery renders only fixed notices and ignores arbitrary or encoded provider text", () => {
  assert.match(loginNotice("#login=failed"), /войти/);
  assert.match(loginNotice("#login=unavailable"), /недоступен/);
  assert.match(loginNotice("#login=busy"), /позже/);
  for (const value of [
    "#login=<script>",
    "#login=failed&state=private",
    "#login=%66ailed",
    "#workspace-main",
  ])
    assert.equal(loginNotice(value), "");
});
