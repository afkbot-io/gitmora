import assert from "node:assert/strict";
import test from "node:test";
import {
  browseQuery,
  navigateEntry,
  packQuery,
  sameSelection,
  observedNumber,
} from "../../web/src/domain/history.js";

const project = "11111111-1111-4111-8111-111111111111";
const repository = "22222222-2222-4222-8222-222222222222";
const session = "33333333-3333-4333-8333-333333333333";
test("source navigation preserves exact project, repository and expected head, with typed cursors", () => {
  assert.equal(observedNumber(7), "7");
  assert.match(observedNumber(9007199254740993), /диапазона/);
  assert.deepEqual(browseQuery(project, "session", ""), {
    project_id: project,
    kind: "session",
    limit: 20,
  });
  assert.throws(() => browseQuery(project, "invalid" as never, repository));
  const q = {
    project_id: project,
    repository_id: repository,
    merge_request_iid: 7,
    expected_head_sha: "a".repeat(40),
  };
  const entry = {
    query: q,
    cursor: { repository_id: repository, iid: "7" },
    title: "MR",
    observed_at: "2026-10-30T10:00:00Z",
  };
  assert.deepEqual(navigateEntry(entry, project), q);
  assert.throws(() =>
    navigateEntry({ ...entry, query: { ...q, project_id: session } }, project),
  );
  assert.throws(() =>
    navigateEntry(
      {
        ...entry,
        cursor: { repository_id: repository, iid: "9007199254740993" },
      },
      project,
    ),
  );
  assert.deepEqual(packQuery(q), { ...q, limit: 6 });
  assert(sameSelection(q, { ...q, limit: 6 }));
  assert(!sameSelection(q, { ...q, expected_head_sha: "b".repeat(40) }));
});
test("selection accepts exactly one explicit session, commit or MR and rejects ambiguity", () => {
  assert.equal(
    packQuery({
      project_id: project,
      repository_id: repository,
      session_id: session,
    }).session_id,
    session,
  );
  for (const invalid of [
    { project_id: project },
    { project_id: project, session_id: session },
    { project_id: project, country_id: session },
    {
      project_id: project,
      repository_id: repository,
      session_id: session,
      merge_request_iid: 7,
    },
    { project_id: project, repository_id: repository, commit_sha: "short" },
    {
      project_id: project,
      repository_id: repository,
      session_id: session,
      merge_requests_after: {
        repository_id: repository,
        iid: 9007199254740993,
      },
    },
    {
      project_id: project,
      repository_id: repository,
      session_id: session,
      capture_after: {
        repository_id: repository,
        session_id: session,
        segment: 0,
      },
    },
  ])
    assert.throws(() => packQuery(invalid));
});
