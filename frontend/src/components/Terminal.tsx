import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";
import { Field, Spinner } from "./ui";
import { api } from "../lib/api";
import { Screen } from "./Screen";

/**
 * A tmux session, to look at and to type into.
 *
 * One component for both places it appears: a card on a board, and the machines
 * page. It fills whatever it is given and can be thrown full-screen, because a
 * terminal in a 300-pixel box is not a terminal.
 *
 * What it shows is what tmux draws — capture-pane, character for character, in
 * a monospace block that scrolls with the output and stays at the bottom unless
 * you scroll up yourself. Keys go the other way with send-keys. It is not a
 * terminal emulator and does not claim to be: no colours, no cursor. What it is
 * for is a long-running thing you want to watch and occasionally tell something.
 *
 * The password is asked for once and held here while the page is open. A
 * machine with an account behind it is never asked.
 */

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

  const body = (
    <div className={full ? "terminal full" : "terminal"}>
      <div className="terminal-bar">
        {inside && !session ? (
          <button className="btn ghost icon" aria-label="Back to the sessions" onClick={() => setInside("")}>
            <Icon name="chevronLeft" size={15} />
          </button>
        ) : null}
        <strong className="mono">{title ?? (inside ? `${machine} · ${inside}` : machine)}</strong>
        <span className="grow" />
        {inside ? (
          <span className="meta">tmux · {inside}</span>
        ) : (
          <button className="btn ghost icon" aria-label="Refresh" onClick={() => void list()}>
            <Icon name="refresh" size={14} />
          </button>
        )}
        <button
          className="btn ghost icon"
          aria-label={full ? "Leave full screen" : "Full screen"}
          title={full ? "Escape" : "Full screen"}
          onClick={() => setFull(!full)}
        >
          <Icon name={full ? "x" : "grid"} size={14} />
        </button>
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
        {onLeave && !full ? (
          <button className="btn ghost icon" aria-label="Close" onClick={onLeave}>
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
                if (e.key !== "Enter") return;
                held.current = password;
                setAsking(false);
                void list();
              }}
            />
          </Field>
          <button
            className="btn primary small"
            onClick={() => {
              held.current = password;
              setAsking(false);
              void list();
            }}
          >
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
              <button key={s.name} className="terminal-session" onClick={() => setInside(s.name)}>
                <Icon name="code" size={15} />
                <span className="grow mono">{s.name}</span>
                <span className="meta">
                  {s.windows}w{s.attached ? " · attached" : ""}
                </span>
                <Icon name="chevronRight" size={14} />
              </button>
            ))}
            {sessions.length === 0 ? <p className="meta">{note || "No sessions."}</p> : null}
            <div className="terminal-new">
              <input
                value={line}
                placeholder="name for a new session"
                onChange={(e) => setLine(e.target.value)}
                onKeyDown={async (e) => {
                  if (e.key !== "Enter" || !line.trim()) return;
                  const name = line.trim();
                  setLine("");
                  await call(`${base}/tmux-new`, { session: name });
                  await list();
                  setInside(name);
                }}
              />
              <button
                className="btn small"
                disabled={!line.trim()}
                onClick={async () => {
                  const name = line.trim();
                  setLine("");
                  await call(`${base}/tmux-new`, { session: name });
                  await list();
                  setInside(name);
                }}
              >
                <Icon name="plus" size={13} /> Start it
              </button>
            </div>
          </div>
        )
      ) : (
        <>
          <Screen
            base={base}
            session={inside}
            password={held.current}
            byAccount={byAccount}
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
          <strong className="mono">{title ?? (session ? `${machine} · ${session}` : machine)}</strong>
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

  return (
    <>
      {full ? (
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
      )}
    </>
  );
}
