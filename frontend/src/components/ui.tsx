// The handful of pieces every page is built from.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ApiError, report, stepUp } from "../lib/api";
import { Icon, type IconName } from "./Icon";

export function Spinner() {
  return <div className="spinner" role="status" aria-label="loading" />;
}

/** Errors are shown. Never a catch that swallows. */
export function ErrorBox({ error, onRetry }: { error: ApiError | Error | null; onRetry?: () => void }) {
  // A fault on the server, or no answer at all, is reported to the server log —
  // the person who can fix it is not the one reading this box. A refusal (a 4xx
  // with a sentence) is not a fault and stays where it is.
  useEffect(() => {
    if (!error) return;
    const status = error instanceof ApiError ? error.status : 0;
    if (status !== 0 && status < 500) return;
    void report({
      message: error.message,
      where: location.pathname + location.search,
      stack: (error instanceof ApiError ? `status ${error.status} ${error.code}` : error.stack) ?? "",
    });
  }, [error]);

  if (!error) return null;
  const api = error instanceof ApiError ? error : null;
  return (
    <div className="error" role="alert">
      <Icon name="alert" />
      <div style={{ flex: 1 }}>
        <div className="what">{api?.status ? `Something went wrong (${api.status})` : "Something went wrong"}</div>
        <div>{error.message}</div>
        {api?.detail ? <pre className="block" style={{ marginTop: 8 }}>{JSON.stringify(api.detail, null, 2)}</pre> : null}
      </div>
      {onRetry ? (
        <button className="btn small" onClick={onRetry}>
          <Icon name="refresh" size={15} /> Try again
        </button>
      ) : null}
    </div>
  );
}

export function Empty({ children, icon = "box" }: { children: ReactNode; icon?: IconName }) {
  return (
    <div className="empty">
      <Icon name={icon} size={26} />
      <div style={{ marginTop: 8 }}>{children}</div>
    </div>
  );
}

export function Modal({
  title,
  children,
  onClose,
  footer,
  wide,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  footer?: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" style={wide ? { width: "min(1180px, 100%)" } : undefined} role="dialog" aria-modal="true">
        <header>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12 }}>
            <h2>{title}</h2>
            <button className="btn ghost icon" onClick={onClose} aria-label="Close">
              <Icon name="x" />
            </button>
          </div>
        </header>
        <div className="body">{children}</div>
        {footer ? <footer>{footer}</footer> : null}
      </div>
    </div>
  );
}

/** A menu that is visible without a mouse — it sits on the object itself. */
export function Menu({
  items,
  label = "Menu",
}: {
  items: ({ label: string; icon?: IconName; onClick: () => void; danger?: boolean } | "separator")[];
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div className="menu-wrap" ref={wrap}>
      <button
        className="btn ghost icon"
        aria-label={label}
        aria-expanded={open}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setOpen((v) => !v);
        }}
      >
        <Icon name="more" />
      </button>
      {open ? (
        <div className="menu">
          {items.map((item, i) =>
            item === "separator" ? (
              <hr key={i} />
            ) : (
              <button
                key={i}
                className={item.danger ? "danger" : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  setOpen(false);
                  item.onClick();
                }}
              >
                {item.icon ? <Icon name={item.icon} size={15} /> : null}
                {item.label}
              </button>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}

/**
 * A label and the thing it names.
 *
 * The label is a word or two. Whatever else is worth knowing hangs off it as a
 * tooltip rather than a paragraph under every input — a dialog of explanations
 * is a dialog nobody reads.
 */
export function Field({
  label,
  hint,
  required,
  optional,
  children,
}: {
  label: string;
  hint?: ReactNode;
  /** Nothing works without it. */
  required?: boolean;
  /** Fine to leave empty — said out loud where that is not obvious. */
  optional?: boolean;
  children: ReactNode;
}) {
  // A short hint is a tooltip on the label, which is underlined so it is
  // findable — not a question mark, because the label is not a question.
  const explained = typeof hint === "string" ? hint : undefined;
  return (
    <div className="field">
      <label title={explained} className={explained ? "explained" : undefined}>
        {label}
        {required ? <span className="needed" aria-label="required">•</span> : null}
        {optional && !required ? <span className="spare">optional</span> : null}
      </label>
      {children}
      {hint && !explained ? <div className="hint">{hint}</div> : null}
    </div>
  );
}

/**
 * A heading with a hairline over it. Settings grew into a long column of
 * unrelated decisions; this says where one subject ends and the next begins.
 */
/**
 * Something long that does not have to be on screen: a run's log, the output of
 * a command, a list of three hundred files.
 *
 * Closed by default, and the summary says what is inside — how many lines, how
 * many entries — so nobody has to open it to find out whether it is worth
 * opening. Built on <details>, so the keyboard and the browser's own find
 * already work.
 */
export function Fold({
  title,
  hint,
  open,
  children,
}: {
  title: string;
  hint?: string;
  open?: boolean;
  children: ReactNode;
}) {
  return (
    <details className="fold" open={open}>
      <summary>
        <span className="fold-title">{title}</span>
        {hint ? <span className="meta">{hint}</span> : null}
      </summary>
      <div className="fold-body">{children}</div>
    </details>
  );
}

export function Section({ title }: { title: string }) {
  return <div className="section-line">{title}</div>;
}

// ----------------------------------------------------------------- asking

/**
 * The three questions an app asks: "are you sure", "what should it be called",
 * and "here is what happened".
 *
 * The browser has all three built in, and they look like 1998 and appear
 * wherever the browser feels like putting them. These are the same three
 * questions in this app's own window — one provider, one promise each, and the
 * calling code reads the same as it did with confirm() and prompt().
 */
type Question =
  | {
      kind: "confirm";
      title: string;
      body?: ReactNode;
      confirmLabel?: string;
      danger?: boolean;
      resolve: (ok: boolean) => void;
    }
  | {
      kind: "text";
      title: string;
      label: string;
      body?: ReactNode;
      value: string;
      placeholder?: string;
      secret?: boolean;
      resolve: (value: string | null) => void;
    }
  | { kind: "tell"; title: string; body?: ReactNode; resolve: (ok: boolean) => void };

export interface Ask {
  /** "Are you sure?" — true when they said yes. */
  confirm(options: {
    title: string;
    body?: ReactNode;
    confirmLabel?: string;
    danger?: boolean;
  }): Promise<boolean>;
  /** "What should it be called?" — null when they backed out. */
  text(options: {
    title: string;
    label: string;
    body?: ReactNode;
    value?: string;
    placeholder?: string;
    secret?: boolean;
  }): Promise<string | null>;
  /** "Here is what happened." */
  tell(options: { title: string; body?: ReactNode }): Promise<boolean>;
}

const AskContext = createContext<Ask>({
  confirm: async () => false,
  text: async () => null,
  tell: async () => true,
});

export function useAsk(): Ask {
  return useContext(AskContext);
}

export function AskProvider({ children }: { children: ReactNode }) {
  const [question, setQuestion] = useState<Question | null>(null);
  const [typed, setTyped] = useState("");

  const ask = useMemo<Ask>(
    () => ({
      confirm: (options) =>
        new Promise<boolean>((resolve) => setQuestion({ kind: "confirm", ...options, resolve })),
      text: (options) =>
        new Promise<string | null>((resolve) => {
          setTyped(options.value ?? "");
          setQuestion({ kind: "text", value: options.value ?? "", ...options, resolve });
        }),
      tell: (options) => new Promise<boolean>((resolve) => setQuestion({ kind: "tell", ...options, resolve })),
    }),
    [],
  );

  const close = (answer: boolean | string | null) => {
    const current = question;
    setQuestion(null);
    setTyped("");
    if (!current) return;
    if (current.kind === "text") current.resolve(answer as string | null);
    else current.resolve(Boolean(answer));
  };

  return (
    <AskContext.Provider value={ask}>
      {children}
      {question ? (
        <Modal
          title={question.title}
          onClose={() => close(question.kind === "text" ? null : false)}
          footer={
            question.kind === "tell" ? (
              <button className="btn primary" onClick={() => close(true)}>
                Right
              </button>
            ) : (
              <>
                <button className="btn" onClick={() => close(question.kind === "text" ? null : false)}>
                  Cancel
                </button>
                <button
                  className={question.kind === "confirm" && question.danger ? "btn danger" : "btn primary"}
                  disabled={question.kind === "text" && !typed.trim()}
                  onClick={() => close(question.kind === "text" ? typed : true)}
                >
                  {question.kind === "confirm" ? question.confirmLabel ?? "Yes" : "Save"}
                </button>
              </>
            )
          }
        >
          {question.body ? <div className="ask-body">{question.body}</div> : null}
          {question.kind === "text" ? (
            <Field label={question.label}>
              <input
                autoFocus
                type={question.secret ? "password" : "text"}
                value={typed}
                placeholder={question.placeholder}
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && typed.trim()) close(typed);
                }}
              />
            </Field>
          ) : null}
        </Modal>
      ) : null}
    </AskContext.Provider>
  );
}

// --------------------------------------------------------------- step-up

type StepUpRequest = { action: string; resolve: (ok: boolean) => void };
const StepUpContext = createContext<(action: string) => Promise<boolean>>(async () => false);

/**
 * Sensitive steps ask for the password again even in an open session. The
 * dialog is global: any request that comes back with step_up_required can open
 * it and then repeat itself.
 */
export function StepUpProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<StepUpRequest | null>(null);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const ask = useCallback((action: string) => {
    return new Promise<boolean>((resolve) => setRequest({ action, resolve }));
  }, []);

  const close = (ok: boolean) => {
    request?.resolve(ok);
    setRequest(null);
    setPassword("");
    setError(null);
  };

  return (
    <StepUpContext.Provider value={ask}>
      {children}
      {request ? (
        <Modal
          title="Your home-projects password"
          onClose={() => close(false)}
          footer={
            <>
              <button className="btn" onClick={() => close(false)}>
                Cancel
              </button>
              <button
                className="btn primary"
                disabled={busy || !password}
                onClick={async () => {
                  setBusy(true);
                  try {
                    await stepUp(password);
                    close(true);
                  } catch (err) {
                    setError(err instanceof Error ? err.message : String(err));
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Confirm
              </button>
            </>
          }
        >
          <p style={{ marginTop: 0 }}>
            This step needs it: <strong>{request.action}</strong>. Asked for is the password you sign in to
            home-projects with — not the password of the service the account belongs to.
          </p>
          {/* Which server this is. Two instances of the same thing can have
              two different owner passwords, and without this the dialog looks
              identical on both. */}
          <p className="hint" style={{ marginTop: -6 }}>
            on <code className="mono">{window.location.host}</code>
          </p>
          {error ? <div className="error">{error}</div> : null}
          <Field label="Your home-projects password">
            <input
              type="password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && (e.currentTarget.form ?? e.currentTarget).dispatchEvent(new Event("submit"))}
            />
          </Field>
        </Modal>
      ) : null}
    </StepUpContext.Provider>
  );
}

export function useStepUp() {
  return useContext(StepUpContext);
}

/**
 * withStepUp runs an action and, if the server asks for the password, asks and
 * runs it once more.
 */
export function useGuarded() {
  const ask = useStepUp();
  return useCallback(
    async <T,>(action: string, fn: () => Promise<T>): Promise<T> => {
      try {
        return await fn();
      } catch (err) {
        if (err instanceof ApiError && err.needsStepUp) {
          if (await ask(action)) return await fn();
        }
        throw err;
      }
    },
    [ask],
  );
}

/** Deleting is serious: the name has to be typed, and the dialog says what goes. */
export function ConfirmDelete({
  what,
  name,
  details,
  onClose,
  onConfirm,
  downloadUrl,
  extra,
}: {
  what: string;
  name: string;
  details?: ReactNode;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  downloadUrl?: string;
  extra?: ReactNode;
}) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      title={`Delete ${what}`}
      onClose={onClose}
      footer={
        <>
          {downloadUrl ? (
            <a className="btn" href={downloadUrl}>
              <Icon name="download" size={15} /> Download first
            </a>
          ) : null}
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn danger"
            disabled={typed !== name || busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm();
              } finally {
                setBusy(false);
              }
            }}
          >
            Delete for good
          </button>
        </>
      }
    >
      <p style={{ marginTop: 0 }}>
        This cannot be undone. What disappears with <strong>{name}</strong>:
      </p>
      {details}
      {extra}
      <Field label={`Type “${name}” to confirm`}>
        <input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
      </Field>
    </Modal>
  );
}

export function formatDate(value?: string | null, withTime = true) {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "—";
  // Always the 24-hour clock: half past one is 13:30, wherever the browser
  // thinks it is.
  return d.toLocaleString(undefined, {
    hour12: false,
    day: "2-digit",
    month: "short",
    year: d.getFullYear() === new Date().getFullYear() ? undefined : "numeric",
    hour: withTime ? "2-digit" : undefined,
    minute: withTime ? "2-digit" : undefined,
  });
}

export function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[i]}`;
}

export function Copyable({ value }: { value: string }) {
  const [done, setDone] = useState(false);
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <code className="mono" style={{ flex: 1, overflowX: "auto", whiteSpace: "nowrap" }}>
        {value}
      </code>
      <button
        className="btn small"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value);
            setDone(true);
            setTimeout(() => setDone(false), 1500);
          } catch {
            /* clipboard refused — the value is on screen anyway */
          }
        }}
      >
        <Icon name={done ? "check" : "copy"} size={14} /> {done ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
