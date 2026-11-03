/** A saved selection identifies a connection generation, never its encrypted token. */
export interface SavedRepositoryCredential {
  repository_id: string;
  generation: number;
  name: string;
  provider: "github" | "gitlab";
  endpoint: string;
}

export type RepositoryAccessValue =
  | { kind: "new"; token: string }
  | { kind: "saved"; credential: SavedRepositoryCredential }
  | { kind: "public" };

export function repositoryAccessBody(
  value: RepositoryAccessValue,
): Record<string, unknown> {
  if (value.kind === "public") return { anonymous: true };
  if (value.kind === "saved")
    return {
      credential: {
        repository_id: value.credential.repository_id,
        generation: value.credential.generation,
      },
    };
  return { api_token: value.token };
}

/** Keep only an opaque saved reference on retry; clear newly typed secret material. */
export function clearRepositoryToken(
  value: RepositoryAccessValue,
): RepositoryAccessValue {
  return value.kind === "new" ? { kind: "new", token: "" } : value;
}

/** This shapes choices only. The backend verifies the exact configured endpoint before I/O. */
export function credentialMatches(
  credential: SavedRepositoryCredential,
  provider: string,
  raw: string,
): boolean {
  if (credential.provider !== provider) return false;
  if (!raw.trim()) return true;
  try {
    const url = new URL(raw.trim());
    if (url.username || url.password || url.search || url.hash) return false;
    const endpoint = new URL(credential.endpoint);
    if (url.origin !== endpoint.origin) return false;
    const path = url.pathname.replace(/\/$/, "").replace(/\.git$/, "");
    if (provider === "github")
      return (
        path.split("/").slice(0, -2).join("/") ===
        endpoint.pathname.replace(/\/$/, "")
      );
    return (
      path === endpoint.pathname.replace(/\/$/, "") ||
      path.startsWith(endpoint.pathname.replace(/\/$/, "") + "/")
    );
  } catch {
    return false;
  }
}
