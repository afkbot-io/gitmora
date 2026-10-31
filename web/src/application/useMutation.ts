import { useEffect, useRef, useState } from "react";
import { CommandDraft } from "../domain/settings.js";
import { readableError } from "../domain/context.js";
import type { ApiClient, MutationMethod } from "../transport/api.js";

/** Own one scoped command, block double submission and retain metadata replay identity only. */
export function useMutation(client: ApiClient) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const busy = useRef(false);
  const alive = useRef(true);
  const controller = useRef<AbortController | null>(null);
  const draft = useRef(new CommandDraft());
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      controller.current?.abort();
    };
  }, []);
  async function run<T = void>(
    method: MutationMethod,
    path: string,
    body?: Record<string, unknown>,
    catalog = false,
  ): Promise<{ value: T } | null> {
    if (busy.current) return null;
    busy.current = true;
    setPending(true);
    setError("");
    setMessage("");
    controller.current = new AbortController();
    try {
      const payload = catalog
        ? draft.current.prepare(method, path, body ?? {})
        : body;
      const value = await client.mutate<T>(
        method,
        path,
        payload,
        controller.current.signal,
      );
      return alive.current ? { value } : null;
    } catch (e) {
      if (alive.current && !(e instanceof Error && e.name === "AbortError"))
        setError(readableError(e));
      return null;
    } finally {
      busy.current = false;
      if (alive.current) setPending(false);
    }
  }
  return { run, pending, error, message, setMessage };
}
