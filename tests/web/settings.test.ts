import assert from "node:assert/strict";
import test from "node:test";
import { ApiClient, ApiError } from "../../web/src/transport/api.js";
import {
  CommandDraft,
  capabilities,
  providerNumber,
  providerLabel,
} from "../../web/src/domain/settings.js";

test("catalog and connection actions require the real account, scopes and role", () => {
  const operator = {
    user: { operator: true },
    scopes: [
      "directory:read",
      "platform:manage",
      "integration:manage",
      "integration:read",
    ],
  };
  assert.equal(capabilities(operator, { role: "" }).catalog, true);
  assert.equal(capabilities(operator, { role: "" }).integrations, false);
  assert.equal(capabilities(operator, { role: "reader" }).integrations, false);
  assert.equal(capabilities(operator, { role: "admin" }).integrations, true);
  assert.equal(
    capabilities({ ...operator, user: { operator: false } }, { role: "admin" })
      .createProject,
    false,
  );
  assert.equal(
    capabilities(
      { ...operator, scopes: ["directory:read", "directory:manage"] },
      { role: "admin" },
    ).catalog,
    true,
  );
  assert.equal(
    capabilities(
      { ...operator, scopes: ["directory:manage"] },
      { role: "admin" },
    ).catalog,
    false,
  );
});

test("uncertain metadata commands reuse their exact request identity; changed scope/content/version does not", () => {
  let sequence = 0;
  const draft = new CommandDraft(() => "request-" + ++sequence);
  const first = draft.prepare("PATCH", "/v1/projects/a", {
    expected_version: 2,
    name: "Team",
  });
  assert.deepEqual(
    draft.prepare("PATCH", "/v1/projects/a", {
      name: "Team",
      expected_version: 2,
    }),
    first,
  );
  assert.notEqual(
    draft.prepare("PATCH", "/v1/projects/b", {
      expected_version: 2,
      name: "Team",
    }).request_id,
    first.request_id,
  );
  assert.notEqual(
    draft.prepare("PATCH", "/v1/projects/a", {
      expected_version: 3,
      name: "Team",
    }).request_id,
    first.request_id,
  );
});

test("provider project numbers must remain exact safe positive integers", () => {
  assert.equal(providerNumber("42"), 42);
  assert.equal(providerLabel(42), "42");
  assert.match(providerLabel(9007199254740992), /точного/);
  for (const value of ["0", "-1", "1.5", "1e3", "9007199254740992", "42x", ""])
    assert.throws(() => providerNumber(value));
});

test("unsafe empty-body methods still carry CSRF; disconnect accepts an empty 204", async () => {
  let called: RequestInit | undefined;
  const client = new ApiClient(
    async (_path, init) => {
      called = init;
      return new Response(null, { status: 204 });
    },
    () => "workgraph_csrf_dev=wcs_fixture",
  );
  assert.equal(
    await client.mutate("DELETE", "/v1/repositories/repo/gitlab"),
    undefined,
  );
  assert.equal(called?.method, "DELETE");
  assert.equal(
    new Headers(called?.headers).get("X-Workgraph-CSRF"),
    "wcs_fixture",
  );
});

test("version/last-admin errors expose only recognized safe diagnostics", async () => {
  const client = new ApiClient(
    async () =>
      new Response(
        JSON.stringify({
          error: {
            code: "last_project_administrator",
            message: "private-server-content",
          },
        }),
        { status: 409 },
      ),
    () => "",
  );
  await assert.rejects(
    client.mutate("PATCH", "/v1/projects/p", {}),
    (error: unknown) =>
      error instanceof ApiError &&
      error.code === "last_project_administrator" &&
      !error.message.includes("private-server-content") &&
      error.message.includes("администратора"),
  );
});
