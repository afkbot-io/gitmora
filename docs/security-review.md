# Release security review — 0.7.0

Reviewed on 2 November 2026. The audit fixed collector consistency and consent defects and checked the Go/React application, native adapters, source rendering, deployment boundaries and dependencies. This is bounded release evidence, not a penetration-test certificate or a guarantee that no vulnerabilities remain.

## Corrected findings

| ID | Severity | Location | Evidence and correction |
| --- | --- | --- | --- |
| C01 | Medium | `internal/collector/application/frontier.go:214`, `RecordNativeCheckpoints` / `ApplyFrontier` | Hash order was not source chronology, and source binding and endpoint acceptance were separate writes. Exact targets/endpoints are now atomic and applied in verified source order. An unresolved tool from the same source blocks automatic interval consumption. The regression rejects both an unbound gap and a later checkpoint before an earlier pending one, then verifies disjoint message intervals. |
| C02 | Medium | `internal/collector/gitrepo/checkpoint.go:53`, `CheckpointWindow` | A non-ancestor HEAD transition could adopt pre-existing branch history. Bounded pre-tool Git tips now exclude already reachable commits; real branch checkout, new commits and amend are exercised. This is context association, not authorship proof. |
| C03 | Medium | `internal/collector/application/recover_ranges.go:63`, `recordedCommitOutput` | Recovery could bridge an unresolved intermediate commit into a later known one. Successful unresolved/quiet commit attempts now stop that interval; missing objects and the first endpoint do not establish whole-history ownership. Existing immutable revisions are retained. |
| C04 | Medium | `internal/collector/application/frontier.go:263`, `ApplyFrontier` | Applying a pending checkpoint by ID did not recheck named-agent consent. The transaction now checks current repository/agent policy; a disable/re-enable regression covers this path. |
| C05 | Medium | `internal/collector/gitrepo/repository.go:213`, `Reachable`; `repository.go:323`, `gitOutput.Write` | Unbounded Git stdout could exhaust local memory, and a global negative revision set could erase another pushed ref's new context. Queries now cancel above 16 MiB and ref intervals are unioned independently. An actual oversized child-process regression rejects partial output. |

No new HTTP authorization bypass was confirmed. Negative tests cover cookie/CSRF behavior, role checks, membership/credential revocation, redirects, source HTML, provider authority and TLS hostname rejection. Passing these scenarios does not certify every route or deployment.

## Dependency inventory and residual risk

`npm audit` reported zero findings, and Go `govulncheck` reported no reachable vulnerabilities for `./cmd/... ./internal/...` using the selected Go toolchain. The application image scan reported no HIGH/CRITICAL entries. Preserve the scanner database date and image digest with each deployment: these results can change as advisories appear.

The optimized account-provider image contains these system inventory findings. They are **not globally suppressed**:

| ID | Scanner severity | Assessment for the reviewed image |
| --- | --- | --- |
| CVE-2026-91403 | HIGH | Red Hat states that RHEL OpenJDK uses the system CA package and tracks the effective correction through CA updates. The image has `ca-certificates-2026.2.92_v9.0.319-90.3.el9_8`; Java's actual trust store does not contain the AAA Certificate Services or emSign ECC Root CA G3 aliases. An emSign G1 root is a different certificate. This is specific image evidence, not a waiver for other CA stores. |
| CVE-2026-119586 | HIGH | The reported trigger is the SQLite command-line `.recover` workflow. The image supplies `sqlite-libs`, but no `sqlite3` shell executable. Gitmora's server and identity service use PostgreSQL. Do not add the affected shell or pipe recovery output into it. |
| CVE-2026-119373 | HIGH | **Residual dependency finding:** `sqlite-libs-3.34.1-11.el9_8` has FTS5 enabled and is required by `nss-softokn`. No fixed vendor RPM was reported by the scanner. No application interface for attacker-controlled FTS5 SQL/database input was found; the retained library belongs to NSS, not the Gitmora search index. This narrows exposure but does not patch the library. Keep the provider confined, do not introduce arbitrary SQLite imports/SQL, and rebuild when the vendor provides a correction. Do not delete an NSS dependency or replace its ABI with an untested custom build merely to clear the scanner. |

The provider runs without root privileges, with a read-only root, bounded writable tmpfs, dropped capabilities and private administration. These controls reduce exposure; they do not make a vulnerable dependency safe in every use. Custom extensions, FIPS configurations, imported NSS databases and added command tools require a new assessment.

Primary vendor context: [Red Hat's OpenJDK assessment](https://access.redhat.com/security/cve/cve-2026-91403), [CA compromise and updates](https://access.redhat.com/security/vulnerabilities/RHSB-2026-012). SQLite finding details come from the retained image scanner inventory; verify vendor status again before rebuilding.

## Verification and operating limits

Use [the development gates](development.md), including `make verify-release` and a separate `make smoke-load`. Native tests run actual Codex/OpenCode protocols with explicitly labelled model/provider fixtures. Phone checks use browser emulation. Fixtures do not prove arbitrary upstream-service/version compatibility or production capacity.

The local schema-6 upgrade intentionally prevents old clients from reopening the ledger. Stop old agents/watch processes and back up private state before upgrading. Reinstall the OpenCode helper with the new CLI. Old immutable source revisions remain retained; no automatic erasure/retention policy is claimed. Legacy recovery is conservative and may leave gaps. Source fidelity and commit existence do not establish semantic authorship.

Public signup remains an operator choice. SMTP password recovery, automatic external ACL mirroring, artifact signing/notarization and unattended upgrades are outside this release. Use the documented account recovery and backup procedures; do not infer those capabilities from a successful signup or restore fixture.
