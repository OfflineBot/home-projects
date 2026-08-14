import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../../components/Icon";
import { Field, Modal, Spinner, useAsk } from "../../components/ui";
import { api } from "../../lib/api";
import type { CardProps } from "../../components/board/cards";

/**
 * A terminal on the board.
 *
 * A tmux session on another machine, live: what is on its screen, and a line to
 * type into it. With a session named, the card is that session; without one, it
 * lists what is there and lets you go into any of them or start another — which
 * is what makes a tab full of these a terminal page.
 *
 * The password is asked for once and held in this card for as long as the page
 * is open, and nowhere else. A machine with an account behind it is never asked.
 */

interface Session {
  name: string;
  windows: string;
  attached: boolean;
  created: string;
}

export default function TerminalCard({ options }: CardProps) {
  const project = String(options.projectId ?? "");
  const machine = String(options.machine ?? "");
  const fixed = String(options.session ?? "");
  const base = project && machine
    ? `/api/projects/${project}/machines/${encodeURIComponent(machine)}`
    : "";

  const ask = useAsk();
  const [password, setPassword] = useState("");
  const [asking, setAsking] = useState(false);
  const [needsPassword, setNeedsPassword] = useState(false);
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const [inside, setInside] = useState(fixed);
  const [screen, setScreen] = useState("");
  const [line, setLine] = useState("");
  const [note, setNote] = useState("");
  const [live, setLive] = useState(true);
  const held = useRef("");

  const call = useCallback(
    async <T,>(path: string, body: Record<string, unknown> = {}): Promise<T | null> => {
      try {
        const answer = await api<T>(path, { body: { password: held.current, ...body } });
        setNote("");
        setNeedsPassword(false);
        return answer;
      } catch (err) {
        const message = (err as Error).message;
        // The sign-in is the one failure worth asking about rather than showing.
        if (/sign-in|password/i.test(message)) {
          setNeedsPassword(true);
          setAsking(true);
        }
        setNote(message);
        return null;
      }
    },
    [],
  );

  const list = useCallback(async () => {
    if (!base) return;
    const answer = await call<{ sessions: Session[]; note?: string }>(`${base}/tmux`);
    if (answer) {
      setSessions(answer.sessions);
      if (answer.note) setNote(answer.note);
    }
  }, [base, call]);

  const look = useCallback(
    async (session: string) => {
      if (!base || !session) return;
      const answer = await call<{ screen: string }>(`${base}/tmux/${encodeURIComponent(session)}`, {
        lines: 200,
      });
      if (answer) setScreen(answer.screen);
    },
    [base, call],
  );

  useEffect(() => {
    held.current = password;
  }, [password]);

  useEffect(() => {
    if (!base) return;
    if (inside) void look(inside);
    else void list();
    // Once when it appears; the timer below keeps it current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, inside, password]);

  useEffect(() => {
    if (!inside || !live) return;
    const timer = setInterval(() => void look(inside), 4000);
    return () => clearInterval(timer);
  }, [inside, live, look]);

  if (!base) return <div className="meta">This card has no machine yet.</div>;

  return (
    <div className="card-terminal">
      <div className="card-terminal-bar">
        {inside && !fixed ? (
          <button className="btn ghost icon" aria-label="Back to the sessions" onClick={() => setInside("")}>
            <Icon name="chevronLeft" size={14} />
          </button>
        ) : null}
        <strong className="mono">{options.title || (inside ? `${machine} · ${inside}` : machine)}</strong>
        <span className="grow" />
        {inside ? (
          <label className="check" style={{ margin: 0 }}>
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
            <span className="meta">live</span>
          </label>
        ) : null}
        <button
          className="btn ghost icon"
          aria-label="Refresh"
          onClick={() => (inside ? void look(inside) : void list())}
        >
          <Icon name="refresh" size={13} />
        </button>
      </div>

      {!inside ? (
        sessions === null ? (
          <Spinner />
        ) : (
          <div className="card-terminal-list">
            {sessions.map((s) => (
              <button key={s.name} className="card-terminal-session" onClick={() => setInside(s.name)}>
                <Icon name="code" size={14} />
                <span className="grow mono">{s.name}</span>
                <span className="meta">{s.windows}w{s.attached ? " · attached" : ""}</span>
              </button>
            ))}
            {sessions.length === 0 ? <div className="meta">{note || "No sessions."}</div> : null}
            <button
              className="btn small"
              onClick={async () => {
                const name = await ask.text({ title: "New session", label: "Name", placeholder: "work" });
                if (!name) return;
                await call(`${base}/tmux-new`, { session: name });
                await list();
                setInside(name);
              }}
            >
              <Icon name="plus" size={13} /> New session
            </button>
          </div>
        )
      ) : (
        <>
          <pre className="tmux-screen">{screen || " "}</pre>
          <form
            className="tmux-type"
            onSubmit={async (e) => {
              e.preventDefault();
              const keys = line;
              setLine("");
              const answer = await call<{ screen: string }>(
                `${base}/tmux/${encodeURIComponent(inside)}/keys`,
                { keys, enter: true },
              );
              if (answer) setScreen(answer.screen);
            }}
          >
            <span className="mono meta">$</span>
            <input
              className="mono"
              value={line}
              onChange={(e) => setLine(e.target.value)}
              placeholder="a line for this session"
            />
            <button className="btn small" disabled={!line.trim()}>Send</button>
            <button
              type="button"
              className="btn small ghost"
              title="Ctrl-C"
              onClick={async () => {
                const answer = await call<{ screen: string }>(
                  `${base}/tmux/${encodeURIComponent(inside)}/keys`,
                  { keys: "C-c", enter: false },
                );
                if (answer) setScreen(answer.screen);
              }}
            >
              ^C
            </button>
          </form>
        </>
      )}

      {note && !needsPassword ? <div className="meta">{note}</div> : null}

      {asking ? (
        <Modal
          title={`Password for ${machine}`}
          onClose={() => setAsking(false)}
          footer={
            <>
              <button className="btn" onClick={() => setAsking(false)}>Cancel</button>
              <button
                className="btn primary"
                onClick={() => {
                  held.current = password;
                  setAsking(false);
                  if (inside) void look(inside);
                  else void list();
                }}
              >
                Go on
              </button>
            </>
          }
        >
          <p className="meta" style={{ marginTop: 0 }}>
            That machine's password, not this server's. Kept while the page is open.
          </p>
          <Field label="Password">
            <input
              type="password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== "Enter") return;
                held.current = password;
                setAsking(false);
                if (inside) void look(inside);
                else void list();
              }}
            />
          </Field>
        </Modal>
      ) : null}
    </div>
  );
}
