import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { Field, Spinner } from "../ui";
import { api } from "../../lib/api";
import { Emulator } from "./Emulator";

/**
 * A terminal, with everything around it: which session, the password, full
 * screen, and the emulator in the middle.
 *
 * There is one of these, and every place that shows a terminal uses it — the
 * machines page, a card on a board, a tag in a hand-written page. That is the
 * point: a terminal that looks wrong looks wrong in exactly one file, and
 * fixing it fixes it everywhere. Nothing else may open a socket of its own.
 *
 * It has no height of its own. It fills what it is given, whether that is a
 * card, a slot in a page or the whole screen, and it stays usable when what it
 * is given is small: the bar does not wrap, the title gives way before the
 * buttons do.
 *
 * The password is asked for inside the terminal, never over the page, and the
 * key in the bar asks again at any time — a box that can only be dismissed once
 * is a box that locks you out.
 */

/** The type size from last time, if the browser kept it. */
function kept(): number {
  try {
    const size = Number(localStorage.getItem("terminal.size"));
    return size >= 9 && size <= 24 ? size : 0;
  } catch {
    return 0;
  }
}

export interface Session {
  name: string;
  windows: string;
  attached: boolean;
  created: string;
}

export function Terminal({
  base,
  machine,
  session,
  byAccount,
  onLeave,
  title,
  asButton,
  fontSize,
  editing,
}: {
  /** /api/projects/:id/machines/:name */
  base: string;
  machine: string;
  /** A session to open at once; empty lists them. */
  session?: string;
  /** The machine signs in with a stored account, so nothing is asked. */
  byAccount?: boolean;
  onLeave?: () => void;
  title?: string;
  /** A button on the board; the terminal itself opens over the page. */
  asButton?: boolean;
  /** The size this card was set up with. The buttons in the bar still win. */
  fontSize?: number;
  /**
   * The board is being arranged. Nothing is connected and nothing is asked —
   * laying out a page is not the moment to be asked for a machine's password.
   */
  editing?: boolean;
}) {
  const [password, setPassword] = useState("");
  const [asking, setAsking] = useState(false);
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const [inside, setInside] = useState(session ?? "");
  const [line, setLine] = useState("");
  const [note, setNote] = useState("");
  const [full, setFull] = useState(false);
  const [opened, setOpened] = useState(!asButton);
  // The password lives in state as well as in the ref. The ref is what the
  // tmux calls read; the state is what the emulator is given — and a ref does
  // not re-render, which is why one wrong password used to lock the terminal
  // for good: the corrected one never reached the socket.
  const [secret, setSecret] = useState("");
  // How large the type is. Remembered, because a terminal that is set up the
  // way somebody likes it and then forgets is worse than one that never asked.
  const [size, setSize] = useState(() => kept() || fontSize || 0);
  // What the terminal actually came out as. Shown, because "it is too narrow"
  // is a different fault depending on whether that says 80 or 200.
  const [measured, setMeasured] = useState("");
  const resize = (to: number) => {
    const next = Math.min(24, Math.max(9, to));
    setSize(next);
    try {
      localStorage.setItem("terminal.size", String(next));
    } catch {
      // A browser that refuses to remember is still a working terminal.
    }
  };
  const [tries, setTries] = useState(0);
  const held = useRef("");

  const call = useCallback(
    async <T,>(path: string, body: Record<string, unknown> = {}): Promise<T | null> => {
      try {
        const answer = await api<T>(path, { body: { password: held.current, ...body } });
        setNote("");
        return answer;
      } catch (err) {
        const message = (err as Error).message;
        if (/sign-in|password/i.test(message) && !byAccount) setAsking(true);
        else setNote(message);
        return null;
      }
    },
    [byAccount],
  );

  const list = useCallback(async () => {
    const answer = await call<{ sessions: Session[]; note?: string }>(`${base}/tmux`);
    if (answer) {
      setSessions(answer.sessions);
      setNote(answer.note ?? "");
    }
  }, [base, call]);

  useEffect(() => {
    if (editing) return;
    if (!inside) void list();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, inside, editing]);

  useEffect(() => {
    if (!full) return;
    const escape = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setFull(false);
      if (asButton) setOpened(false);
    };
    window.addEventListener("keydown", escape);
    return () => window.removeEventListener("keydown", escape);
  }, [full, asButton]);

  const start = async (name: string) => {
    setLine("");
    setNote("");
    await call(`${base}/tmux-new`, { session: name });
    await list();
    setInside(name);
  };

  const signIn = () => {
    held.current = password;
    setSecret(password);
    // Counted, so that trying the same password again really does try again
    // rather than being a state that did not change.
    setTries((n) => n + 1);
    setAsking(false);
    void list();
  };

  const shown = title ?? (inside ? `${machine} · ${inside}` : machine);

  const body = (
    <div className={full ? "terminal full" : "terminal"}>
      <div className="terminal-bar">
        {inside && !session ? (
          <button className="btn ghost icon" aria-label="Back to the sessions" onClick={() => setInside("")}>
            <Icon name="chevronLeft" size={15} />
          </button>
        ) : null}
        <strong className="mono terminal-title" title={measured ? `${measured} characters` : undefined}>
          {shown}
        </strong>
        <span className="grow" />
        {!inside ? (
          <button className="btn ghost icon" aria-label="Refresh" onClick={() => void list()}>
            <Icon name="refresh" size={14} />
          </button>
        ) : null}
        {inside ? (
          <>
            <button
              className="btn ghost icon"
              aria-label="Smaller type"
              title="Smaller"
              onClick={() => resize((size || 13) - 1)}
            >
              <span className="terminal-size">A−</span>
            </button>
            <button
              className="btn ghost icon"
              aria-label="Larger type"
              title="Larger"
              onClick={() => resize((size || 13) + 1)}
            >
              <span className="terminal-size big">A+</span>
            </button>
          </>
        ) : null}
        {!byAccount ? (
          <button
            className="btn ghost icon"
            aria-label="Password"
            title="That machine's password"
            onClick={() => setAsking((a) => !a)}
          >
            <Icon name="key" size={14} />
          </button>
        ) : null}
        <button
          className="btn ghost icon"
          aria-label={full ? "Leave full screen" : "Full screen"}
          title={full ? "Back into the page" : "Full screen"}
          onClick={() => setFull(!full)}
        >
          <Icon name={full ? "chevronDown" : "grid"} size={14} />
        </button>
        {/* Closing has to close. A terminal opened from a button used to leave
            full screen and stay behind in a card the size of the button, with
            no way out of it. */}
        {onLeave || asButton ? (
          <button
            className="btn ghost icon"
            aria-label="Close"
            title="Close"
            onClick={() => {
              setFull(false);
              setOpened(!asButton);
              onLeave?.();
            }}
          >
            <Icon name="x" size={15} />
          </button>
        ) : null}
      </div>

      {asking ? (
        <div className="terminal-signin">
          <Field label="SSH password" required>
            <input
              type="password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") signIn();
              }}
            />
          </Field>
          <button className="btn primary small" onClick={signIn}>
            Go on
          </button>
          <p className="meta">That machine's password, not this server's. Kept while the page is open.</p>
        </div>
      ) : null}

      {!inside ? (
        sessions === null ? (
          <Spinner />
        ) : (
          <div className="terminal-sessions">
            {sessions.map((s) => (
              <div key={s.name} className="terminal-session">
                <button
                  className="terminal-session-open"
                  onClick={() => {
                    setNote("");
                    setInside(s.name);
                  }}
                >
                  <Icon name="code" size={15} />
                  <span className="grow mono">{s.name}</span>
                  <span className="meta">
                    {s.windows}w{s.attached ? " · attached" : ""}
                  </span>
                  <Icon name="chevronRight" size={14} />
                </button>
                {/* Ending one belongs where they are listed. Otherwise the only
                    way to close a session is to attach to it and type exit. */}
                <button
                  className="btn ghost icon"
                  aria-label={`End ${s.name}`}
                  title="End this session"
                  onClick={async () => {
                    await call(`${base}/tmux/${encodeURIComponent(s.name)}/kill`);
                    await list();
                  }}
                >
                  <Icon name="trash" size={13} />
                </button>
              </div>
            ))}
            {sessions.length === 0 ? <p className="meta">{note || "No sessions."}</p> : null}
            <div className="terminal-new">
              <input
                value={line}
                placeholder="name for a new session"
                onChange={(e) => setLine(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && line.trim()) void start(line.trim());
                }}
              />
              <button className="btn small" disabled={!line.trim()} onClick={() => void start(line.trim())}>
                <Icon name="plus" size={13} /> Start it
              </button>
            </div>
          </div>
        )
      ) : (
        <>
          <Emulator
            key={tries}
            base={base}
            session={inside}
            password={secret}
            byAccount={byAccount}
            size={size}
            onMeasured={(cols, rows) => setMeasured(`${cols}×${rows}`)}
            onNeedsPassword={() => setAsking(true)}
          />
          {note ? <div className="meta terminal-note">{note}</div> : null}
        </>
      )}
    </div>
  );

  // The board is being arranged: a still picture. No socket is opened, nothing
  // is asked for. Being asked for a machine's password while moving a card
  // around is the kind of interruption that makes a page not worth arranging.
  if (editing) {
    return (
      <div className="terminal quiet">
        <div className="terminal-bar">
          <Icon name="code" size={15} />
          <strong className="mono terminal-title">{shown}</strong>
        </div>
        <p className="meta">Opens when you leave edit mode.</p>
      </div>
    );
  }

  // As a button: one press and the terminal is over the page, full size.
  if (asButton && !opened) {
    return (
      <button
        className="btn primary terminal-quick"
        onClick={() => {
          setOpened(true);
          setFull(true);
        }}
      >
        <Icon name="code" size={15} /> {title ?? `${machine}${session ? " · " + session : ""}`}
      </button>
    );
  }

  return full ? (
    <div
      className="terminal-backdrop"
      onClick={(e) => {
        if (e.target !== e.currentTarget) return;
        setFull(false);
        if (asButton) setOpened(false);
      }}
    >
      {body}
    </div>
  ) : (
    body
  );
}
