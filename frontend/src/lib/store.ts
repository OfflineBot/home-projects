// The little state there is.
//
// No state framework: useSyncExternalStore is enough for "who is signed in"
// and "what can this server do", and everything else is fetched where it is
// shown.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { ApiError, api, hasToken, onAuthChange, refresh, type Meta, type User } from "./api";

type Session = { user: User | null; ready: boolean };

let session: Session = { user: null, ready: false };
const sessionListeners = new Set<() => void>();

function setSession(next: Session) {
  session = next;
  sessionListeners.forEach((fn) => fn());
}

export function useSession() {
  const value = useSyncExternalStore(
    (fn) => {
      sessionListeners.add(fn);
      const off = onAuthChange(fn);
      return () => {
        sessionListeners.delete(fn);
        off();
      };
    },
    () => session,
    () => session,
  );
  return value;
}

export function setUser(user: User | null) {
  setSession({ user, ready: true });
}

/** startSession runs once when the app loads: renew the token, then ask who we are. */
export async function startSession() {
  if (!hasToken()) await refresh();
  if (!hasToken()) {
    setSession({ user: null, ready: true });
    return;
  }
  try {
    const me = await api<{ user: User | null }>("/api/auth/me");
    setSession({ user: me.user, ready: true });
  } catch {
    setSession({ user: null, ready: true });
  }
}

// ------------------------------------------------------------------- meta

let meta: Meta | null = null;
const metaListeners = new Set<() => void>();

export async function loadMeta() {
  meta = await api<Meta>("/api/meta");
  metaListeners.forEach((fn) => fn());
}

export function useMeta(): Meta | null {
  return useSyncExternalStore(
    (fn) => {
      metaListeners.add(fn);
      return () => {
        metaListeners.delete(fn);
      };
    },
    () => meta,
    () => meta,
  );
}

// ------------------------------------------------------------------ fetch

export type Query<T> = {
  data: T | undefined;
  error: ApiError | null;
  loading: boolean;
  reload: () => void;
};

/**
 * useQuery fetches and re-fetches. After every action the view refreshes
 * itself — there are no reloads in this app, so `reload` is what every mutation
 * calls when it is done.
 */
export function useQuery<T>(path: string | null, deps: unknown[] = []): Query<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(path !== null);
  const [tick, setTick] = useState(0);
  const current = useRef(0);

  useEffect(() => {
    if (!path) {
      setLoading(false);
      return;
    }
    const run = ++current.current;
    setLoading(true);
    api<T>(path)
      .then((result) => {
        if (run !== current.current) return;
        setData(result);
        setError(null);
      })
      .catch((err) => {
        if (run !== current.current) return;
        setError(err instanceof ApiError ? err : new ApiError(0, "network", String(err)));
      })
      .finally(() => {
        if (run === current.current) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, tick, ...deps]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

/** useAction wraps a mutation: one place for "running" and for the error. */
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const run = useCallback(async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, "network", String(err)));
      return undefined;
    } finally {
      setBusy(false);
    }
  }, []);

  return { busy, error, setError, run };
}
