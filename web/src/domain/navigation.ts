const views = [
  "context",
  "repositories",
  "settings",
  "catalog",
  "accounts",
  "work",
  "keys",
  "bindings",
  "setup",
  "preferences",
] as const;
export type WorkspaceView = (typeof views)[number];

/** Only local, known workspace locations are accepted; CLI consent has its own hash namespace. */
export function workspaceView(
  hash: string,
  fallback: WorkspaceView = "context",
): WorkspaceView {
  const value = new URLSearchParams(hash.replace(/^#/, "")).get("view");
  return views.find((view) => view === value) ?? fallback;
}
