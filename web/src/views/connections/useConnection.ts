import { useEffect, useState } from "react";
import { LatestRequest } from "../../application/latest.js";
import { readableError } from "../../domain/context.js";
import { ApiError } from "../../transport/api.js";
import type { ApiClient } from "../../transport/api.js";
import { aborted } from "../shared.js";

/** A missing connection differs from an unconfigured server or a failed status read. */
export function useConnection<T>(
  client: ApiClient,
  path: string,
  canRead: boolean,
) {
  const [value, setValue] = useState<T | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [missing, setMissing] = useState(false);
  const [requests] = useState(() => new LatestRequest());
  async function refresh() {
    if (!canRead) return;
    const request = requests.begin();
    setLoading(true);
    setError("");
    setValue(null);
    setMissing(false);
    try {
      const result = await client.request<T>(path, undefined, request.signal);
      if (request.current()) setValue(result);
    } catch (e) {
      if (request.current() && !aborted(e)) {
        if (e instanceof ApiError && e.status === 404) setMissing(true);
        else setError(readableError(e));
      }
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void refresh();
    return () => requests.cancel();
  }, [client, path, canRead, requests]);
  function accept(result: T) {
    requests.cancel();
    setLoading(false);
    setMissing(false);
    setError("");
    setValue(result);
  }
  function clear() {
    requests.cancel();
    setLoading(false);
    setValue(null);
    setMissing(false);
    setError("");
  }
  return { value, loading, error, missing, refresh, setValue: accept, clear };
}
