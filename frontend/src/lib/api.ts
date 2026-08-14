// One way in and out of the server.
//
// The access token lives here in memory only — the binding cookie stays
// httpOnly, so a stolen token alone is useless. Every failure comes back as a
// typed error with the server's own message; nothing is swallowed.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly detail?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }

  get needsStepUp() {
    return this.code === "step_up_required";
  }
  get needsPassword() {
    return this.code === "password_required";
  }
  get isReadOnly() {
    return this.code === "read_only";
  }
}

let accessToken: string | null = null;
let refreshing: Promise<boolean> | null = null;
const listeners = new Set<() => void>();

export function onAuthChange(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function announce() {
  listeners.forEach((fn) => fn());
}

export function setToken(token: string | null) {
  accessToken = token;
  announce();
}

export function hasToken() {
  return accessToken !== null;
}

/**
 * An address the browser itself fetches — a download link, a picture, a PDF in
 * a frame. None of those can carry an Authorization header, so the token rides
 * in the query, exactly as the calendar subscriptions do. It is worthless
 * without the httpOnly binding cookie that goes with it.
 */
export function authedUrl(path: string) {
  if (!accessToken) return path;
  return path + (path.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(accessToken);
}

type Options = {
  method?: string;
  body?: unknown;
  raw?: BodyInit;
  headers?: Record<string, string>;
  /** Text instead of JSON — used for .ics downloads and file contents. */
  text?: boolean;
  retry?: boolean;
};

export async function api<T = any>(path: string, options: Options = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", ...options.headers };
  let body: BodyInit | undefined = options.raw;
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(options.body);
  }
  if (accessToken) headers.Authorization = `Bearer ${accessToken}`;

  const response = await fetch(path, {
    method: options.method ?? (body ? "POST" : "GET"),
    headers,
    body,
    credentials: "same-origin",
  });

  // A short-lived token that ran out is renewed once, silently.
  if (response.status === 401 && options.retry !== false && !path.includes("/auth/")) {
    if (await refresh()) return api<T>(path, { ...options, retry: false });
  }

  if (!response.ok) {
    let code = "http_error";
    let message = `${response.status} ${response.statusText}`;
    let detail: unknown;
    try {
      const payload = await response.json();
      if (payload?.error) {
        code = payload.error.code ?? code;
        message = payload.error.message ?? message;
        detail = payload.error.detail;
      }
    } catch {
      /* the body was not JSON; the status text has to do */
    }
    throw new ApiError(response.status, code, message, detail);
  }

  if (response.status === 204) return undefined as T;
  if (options.text) return (await response.text()) as T;
  const type = response.headers.get("content-type") ?? "";
  if (!type.includes("application/json")) return (await response.text()) as T;
  return (await response.json()) as T;
}

/** refresh swaps the httpOnly refresh cookie for a new access token. */
export async function refresh(): Promise<boolean> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        const res = await fetch("/api/auth/refresh", {
          method: "POST",
          credentials: "same-origin",
          headers: { Accept: "application/json" },
        });
        if (!res.ok) {
          setToken(null);
          return false;
        }
        const payload = await res.json();
        setToken(payload.accessToken);
        return true;
      } catch {
        setToken(null);
        return false;
      } finally {
        refreshing = null;
      }
    })();
  }
  return refreshing;
}

export async function login(username: string, password: string, totp?: string) {
  const result = await api<{ accessToken: string; user: User }>("/api/auth/login", {
    body: { username, password, totp },
    retry: false,
  });
  setToken(result.accessToken);
  return result.user;
}

export async function logout() {
  try {
    await api("/api/auth/logout", { method: "POST" });
  } finally {
    setToken(null);
  }
}

/**
 * report tells the server what the browser could not do. It is best-effort and
 * silent: a failure to report a failure helps nobody.
 */
export async function report(what: { message: string; where: string; stack?: string }) {
  try {
    await api("/api/client-errors", { body: what, retry: false });
  } catch {
    /* nothing to be done about it here */
  }
}

/** stepUp confirms the password again for a sensitive step. */
export async function stepUp(password: string) {
  await api("/api/auth/step-up", { method: "POST", body: { password } });
}

// The access token is renewed a little before it runs out, so a long session
// never fails a request over it.
setInterval(() => {
  if (accessToken) void refresh();
}, 12 * 60 * 1000);

// ------------------------------------------------------------------- types

export type Visibility = "private" | "public" | "password";

export interface User {
  id: string;
  username: string;
  displayName: string;
  totpEnabled: boolean;
  isOwner: boolean;
}

export interface Group {
  id: string;
  slug: string;
  title: string;
  description: string;
  visibility: Visibility;
  hasPassword: boolean;
  readOnly: boolean;
  pushWithPassword: boolean;
  color: string;
  icon: string;
  siteProjectId?: string;
  pinned: boolean;
  archived: boolean;
  projectCount: number;
  cloneUrl?: string;
}

export interface Project {
  id: string;
  groupId?: string;
  groupSlug?: string;
  groupTitle?: string;
  slug: string;
  title: string;
  description: string;
  capabilities: string[];
  preset: string;
  defaultTab: string;
  gitTracked: boolean;
  siteRoot?: string;
  visibility: Visibility;
  hasPassword: boolean;
  readOnly: boolean;
  anonWrite: boolean;
  color: string;
  icon: string;
  archived: boolean;
  siteUrl?: string;
  cloneUrl?: string;
}

export interface FileEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  modifiedAt: string;
  mimeType?: string;
  linkId?: string;
  linkedFrom?: string;
  linkProject?: string;
}

export interface Variable {
  projectId: string;
  projectSlug?: string;
  name: string;
  type: string;
  value: any;
  unit: string;
  source: string;
  error?: string;
  updatedAt: string;
}

export interface Derived {
  name: string;
  op: string;
  unit?: string;
  value: any;
  type: string;
  error?: string;
}

export interface Preset {
  key: string;
  title: string;
  description: string;
  icon: string;
  defaultTab: string;
  capabilities: string[];
}

export interface CapabilityInfo {
  name: string;
  title: string;
  icon: string;
  owns: string[];
}

export interface SchedulerKind {
  name: string;
  title: string;
  description: string;
  accountKinds: string[] | null;
  accountRequired: boolean;
  options?: AccountField[];
}

export interface AccountField {
  name: string;
  label: string;
  type: string;
  placeholder?: string;
  required: boolean;
  /** What the field means when nothing was said — the box shows what the server does. */
  default?: unknown;
  hint?: string;
}

export interface AccountKind {
  name: string;
  title: string;
  description: string;
  fields: AccountField[];
  secretLabel?: string;
  locks: boolean;
  secretIsKey?: boolean;
}

export interface Account {
  id: string;
  kind: string;
  title: string;
  config: Record<string, any>;
  state: string;
  hasSecret: boolean;
  needsSecret: boolean;
  attemptInFlight: boolean;
  consumedAt?: string;
  lastOkAt?: string;
  lastError: string;
  schedulerCount: number;
}

export interface Scheduler {
  id: string;
  projectId: string;
  projectSlug?: string;
  accountId?: string;
  accountName?: string;
  title: string;
  kind: string;
  schedule: string;
  targetPath: string;
  options: Record<string, any>;
  enabled: boolean;
  pausedReason: string;
  lastRunAt?: string;
  lastStatus: string;
  nextRun?: string;
  /** True while a run is in flight — a second start is refused, not queued. */
  running?: boolean;
  filterId?: string;
  filterName?: string;
}

export interface FilterRule {
  match: string;
  field?: string;
  to: string;
}

export interface Filter {
  id: string;
  slug: string;
  title: string;
  description: string;
  rules: FilterRule[];
  usedBy: number;
}

export interface SchedulerRun {
  id: number;
  schedulerId: string;
  startedAt: string;
  finishedAt?: string;
  status: string;
  message: string;
  filesChanged: number;
  trigger: string;
  log: string;
}

export interface Meta {
  capabilities: CapabilityInfo[];
  presets: Preset[];
  schedulerKinds: SchedulerKind[];
  accountKinds: AccountKind[];
  actions: { name: string; title: string; description: string; params: string[] }[];
  colors: string[];
  icons: string[];
  publicUrl: string;
  signedIn: boolean;
}

/** A token for a machine. The secret is only ever set in the response that created it. */
export interface Token {
  id: string;
  name: string;
  scope: string;
  projectId?: string;
  groupId?: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
  revokedAt?: string;
  secret: string;
}

export interface Link {
  id: string;
  kind: "folder" | "file";
  sourceProject: string;
  sourceSlug?: string;
  sourceTitle?: string;
  sourcePath: string;
  targetProject: string;
  targetSlug?: string;
  targetPath: string;
}
