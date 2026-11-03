import test from "node:test";
import assert from "node:assert/strict";
import {
  credentialMatches,
  repositoryAccessBody,
  clearRepositoryToken,
  type SavedRepositoryCredential,
} from "../../web/src/domain/repository-access.js";

const github: SavedRepositoryCredential = {
  repository_id: "11111111-1111-4111-8111-111111111111",
  generation: 3,
  name: "Team API",
  provider: "github",
  endpoint: "https://github.com",
};

test("saved token selection sends only a generation reference and clears typed secrets", () => {
  assert.deepEqual(
    repositoryAccessBody({ kind: "saved", credential: github }),
    { credential: { repository_id: github.repository_id, generation: 3 } },
  );
  assert.deepEqual(
    clearRepositoryToken({ kind: "new", token: "fixture-only-secret" }),
    { kind: "new", token: "" },
  );
  assert.deepEqual(repositoryAccessBody({ kind: "public" }), {
    anonymous: true,
  });
});

test("saved choices stay within the provider, custom hostname, port and path prefix", () => {
  assert.equal(
    credentialMatches(github, "github", "https://github.com/team/new.git"),
    true,
  );
  assert.equal(
    credentialMatches(github, "gitlab", "https://github.com/team/new"),
    false,
  );
  assert.equal(
    credentialMatches(
      github,
      "github",
      "https://github.com.evil.invalid/team/new",
    ),
    false,
  );
  const enterprise = {
    ...github,
    endpoint: "https://forge.company.test/github",
  };
  assert.equal(
    credentialMatches(
      enterprise,
      "github",
      "https://forge.company.test/github/team/new",
    ),
    true,
  );
  assert.equal(
    credentialMatches(
      enterprise,
      "github",
      "https://forge.company.test/other/team/new",
    ),
    false,
  );
  const gitlab = {
    ...github,
    provider: "gitlab" as const,
    endpoint: "https://forge.company.test:8443/gitlab",
  };
  assert.equal(
    credentialMatches(
      gitlab,
      "gitlab",
      "https://forge.company.test:8443/gitlab",
    ),
    true,
  );
  assert.equal(
    credentialMatches(
      gitlab,
      "gitlab",
      "https://forge.company.test:8443/gitlab/team/new",
    ),
    true,
  );
  assert.equal(
    credentialMatches(
      gitlab,
      "gitlab",
      "https://forge.company.test:8443/gitlab-other/team/new",
    ),
    false,
  );
  assert.equal(
    credentialMatches(
      gitlab,
      "gitlab",
      "https://forge.company.test/gitlab/team/new",
    ),
    false,
  );
  assert.equal(
    credentialMatches(
      gitlab,
      "gitlab",
      "https://user:password@forge.company.test:8443/gitlab/team/new",
    ),
    false,
  );
});
