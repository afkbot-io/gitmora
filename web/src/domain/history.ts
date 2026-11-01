import { t as translate } from "./locale.js";
import type { Hit, Source } from "./context.js";

export type SourceKind = "session" | "merge_request";
export interface Selection {
  project_id: string;
  repository_id?: string;
  session_id?: string;
  dialogue_id?: string;
  message_after?: MessageCursor;
  commit_sha?: string;
  merge_request_iid?: number;
  expected_head_sha?: string;
  limit?: number;
  capture_after?: CaptureCursor;
  merge_requests_after?: MRCursor;
}
export interface BrowseCursor {
  observed_at?: string;
  repository_id?: string;
  id?: string;
  iid?: string;
}
export interface BrowseEntry {
  cursor: BrowseCursor;
  query: Selection;
  title: string;
  state?: string;
  number?: string;
  agent?: string;
  segment?: number;
  revision?: number;
  digest?: string;
  observed_at: string;
}
export interface BrowsePage {
  schema_version: number;
  query: { project_id: string; repository_id?: string; kind: SourceKind };
  items: BrowseEntry[];
  read_at: string;
  next?: BrowseCursor;
}
export interface CaptureCursor {
  repository_id: string;
  session_id: string;
  segment: number;
}
export interface MRCursor {
  repository_id: string;
  iid: number;
}
export interface CaptureWindow {
  range_status?: string;
  message_ranges?: {
    commit_sha: string;
    start: number;
    end: number;
    basis: string;
  }[];
  source: Source;
  root_session_id: string;
  segment: number;
  agent: string;
  title?: string;
  started_at?: string;
  parent_reference_verified: boolean;
  chain_verified: boolean;
  chain_verification_limited: boolean;
  confirmed_commits: string[];
  confirmed_count: number;
  continuation?: {
    previous_session_id: string;
    previous_revision: number;
    previous_digest: string;
    reason: string;
  };
}
export interface Excerpt {
  text: string;
  truncated: boolean;
}
export interface Pipeline {
  id: number;
  project_id: number;
  sha: string;
  status: string;
  ref: string;
  source?: string;
  url?: string;
  updated_at: string;
  matches_observed_head: boolean;
  matches_expected_head: boolean;
}
export interface Deployment {
  id: number;
  sha: string;
  status: string;
  environment: string;
  updated_at: string;
}
export interface MRContext {
  source_commits: string[];
  source_commit_count: number;
  source_commits_more: boolean;
  association_basis: string;
  repository_id: string;
  iid: number;
  observation: string;
  code_key: string;
  observed_at: string;
  provider_updated_at: string;
  title: string;
  description: Excerpt;
  url: string;
  state: string;
  observed_head_sha: string;
  source_project_id: number;
  source_repository_id?: string;
  source_commits_complete: boolean;
  result_shas: string[];
  expected_head_matches?: boolean;
  pipelines_for_observed_head: Pipeline[];
  pipelines_have_more: boolean;
  deployments_for_result_or_head: Deployment[];
  deployments_have_more: boolean;
}
export interface MessageCursor {
  scope_key?: string;
  rune_offset?: number;
  repository_id: string;
  session_id: string;
  revision: number;
  digest: string;
  segment: number;
  offset: number;
  head?: string;
}
export interface ScopedMessage {
  message_offset?: number;
  source: Source;
  segment: number;
  index: number;
  message: SessionRevision["snapshot"]["messages"][number];
  commit_shas?: string[];
  text_truncated?: boolean;
}
export interface ContextPack {
  scope?: { kind: string; missing_commit_ranges: boolean };
  transcript?: {
    items: ScopedMessage[];
    next?: MessageCursor;
    has_more: boolean;
  };
  schema_version: number;
  query: Selection;
  read_at: string;
  captures: {
    items: CaptureWindow[];
    next?: CaptureCursor;
    omitted_for_permissions: boolean;
  };
  merge_requests: {
    items: MRContext[];
    has_more: boolean;
    next?: MRCursor;
    omitted_for_permissions: boolean;
  };
  evidence: (Hit & { selection: string })[];
  evidence_has_more: boolean;
  pending_sources_in_project: number;
  failed_sources_in_project: number;
  budget_limited: boolean;
}
/** Immutable external evidence identity; provenance remains a claim made by its source. */
export interface ImportOrigin {
  system: "entire" | "git-ai";
  format: string;
  producer_version?: string;
  source_id: string;
  source_session_id?: string;
  agent?: { tool: string; session_id: string; model: string };
  object_id: string;
  ref: string;
  ref_object: string;
  source_digest: string;
  target_commit: string;
  base_commit?: string;
  part: number;
  parts: number;
  line_start?: number;
  line_end?: number;
  source_offset?: number;
  time_kind: "checkpoint_created" | "commit_recorded";
  omitted_blocks?: number;
  external_transcripts?: boolean;
  claims?: {
    path: string;
    start: number;
    end: number;
    key: string;
    kind: "ai" | "known_human";
    agent?: { tool: string; session_id: string; model: string };
    author?: string;
  }[];
}
export interface SessionRevision {
  snapshot: {
    schema_version: number;
    repository_id: string;
    session_id: string;
    revision: number;
    agent: { name: string; model: string; version?: string };
    started_at: string;
    ended_at?: string;
    task?: { country_id: string; task_id: string };
    messages: {
      role: string;
      text: string;
      tool_name?: string;
      kind?: string;
    }[];
    continuation?: { segment: number };
    files: string[];
    commits: string[];
    origin?: ImportOrigin;
  };
  digest: string;
  confirmed_commits: string[];
  parent_reference_verified?: boolean;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const sha = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
/** Legacy pack numeric metadata must never display a rounded identifier as if it were exact. */
export function observedNumber(value: number): string {
  return Number.isSafeInteger(value)
    ? String(value)
    : translate("вне точного диапазона интерфейса");
}
function id(value: string | undefined): boolean {
  return (
    !!value &&
    uuid.test(value) &&
    value !== "00000000-0000-0000-0000-000000000000"
  );
}

/** Navigation sends exactly one selector and pins the expected MR head across refreshes. */
export function packQuery(q: Selection): Selection {
  const selectors = [q.session_id, q.commit_sha, q.merge_request_iid].filter(
    (v) => v !== undefined && v !== "" && v !== 0,
  );
  if (
    !id(q.project_id) ||
    (q.repository_id !== undefined && !id(q.repository_id)) ||
    selectors.length !== 1 ||
    (q.session_id && (!id(q.session_id) || !q.repository_id)) ||
    (q.dialogue_id && (!id(q.dialogue_id) || !!q.session_id)) ||
    (q.commit_sha && (!sha.test(q.commit_sha) || !q.repository_id)) ||
    (q.merge_request_iid !== undefined &&
      (!Number.isSafeInteger(q.merge_request_iid) ||
        q.merge_request_iid < 1 ||
        !q.repository_id)) ||
    (q.expected_head_sha &&
      (!q.merge_request_iid || !sha.test(q.expected_head_sha))) ||
    (q.capture_after &&
      (!id(q.capture_after.repository_id) ||
        !id(q.capture_after.session_id) ||
        !Number.isSafeInteger(q.capture_after.segment) ||
        q.capture_after.segment < 1 ||
        q.capture_after.segment > 1_000_000_000)) ||
    (q.merge_requests_after &&
      (q.merge_request_iid ||
        !id(q.merge_requests_after.repository_id) ||
        !Number.isSafeInteger(q.merge_requests_after.iid) ||
        q.merge_requests_after.iid < 1))
  )
    throw new Error("Источник не содержит однозначной области навигации.");
  return { ...q, limit: 6 };
}
export function browseQuery(
  project: string,
  kind: SourceKind,
  repository: string,
  after?: BrowseCursor,
) {
  if (
    !id(project) ||
    (repository && !id(repository)) ||
    !["session", "merge_request"].includes(kind)
  )
    throw new Error("Некорректная область списка источников.");
  return {
    project_id: project,
    kind,
    ...(repository ? { repository_id: repository } : {}),
    limit: 20,
    ...(after ? { after } : {}),
  };
}
export function navigateEntry(entry: BrowseEntry, project: string): Selection {
  if (entry.query.project_id !== project)
    throw new Error("Сервер вернул источник другого проекта.");
  if (entry.cursor.iid) {
    const iid = Number(entry.cursor.iid);
    if (
      !Number.isSafeInteger(iid) ||
      String(iid) !== entry.cursor.iid ||
      iid !== entry.query.merge_request_iid
    )
      throw new Error("IID превышает точный числовой диапазон интерфейса.");
  }
  const {
    limit: _limit,
    capture_after: _capture,
    merge_requests_after: _mr,
    ...selected
  } = packQuery(entry.query);
  return selected;
}
/** Ignore page size/cursors while requiring the original selector and expected head to match. */
export function sameSelection(a: Selection, b: Selection): boolean {
  return [
    "project_id",
    "repository_id",
    "session_id",
    "dialogue_id",
    "commit_sha",
    "merge_request_iid",
    "expected_head_sha",
  ].every((k) => a[k as keyof Selection] === b[k as keyof Selection]);
}
