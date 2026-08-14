import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/**
 * Other machines: whether they are up, on and off, and their tmux sessions.
 *
 * The SSH password is never sent anywhere but to this server, and this server
 * never keeps it. Here it is kept exactly as long as the page says it is:
 * either for as long as this page is open, or not at all — in which case it is
 * asked for every single time. It is in a variable in this component and in
 * nothing else: not localStorage, not sessionStorage, not the address bar.
 */

interface Machine {
  name: string;
  host: string;
  port?: number;
  user?: string;
  mac?: string;
  broadcast?: string;
  account?: string;
  note?: string;
  up: boolean;
}

interface Session {
  name: string;
  windows: string;
  attached: boolean;
  created: string;
}

interface AccountRow {
  id: string;
  kind: string;
  title: string;
  needsSecret?: boolean;
}

export default function MachinesView({ project }: { project: Project; reload: () => void }) {
  const { data, error, loading, reload } = useQuery<{ machines: Machine[] }>(
    `/api/projects/${project.id}/machines`,
  );
  const [editing, setEditing] = useState<Machine[] | null>(null);
  const accounts = useQuery<{ accounts: AccountRow[] }>("/api/accounts");
  const [open, setOpen] = useState<string | null>(null);
  const [failed, setFailed] = useState<Error | null>(null);

  // The password, and how long it lives.
  const [password, setPassword] = useState("");
  const [keep, setKeep] = useState(true);
  const [asking, setAsking] = useState<null | { why: string; go: (password: string) => void }>(null);

  /**
   * Runs something that needs the sign-in. A machine that has an account
   * behind it needs nothing from anybody: the empty password tells the server
   * to use that account.
   */
  const withPassword = useCallback(
    (why: string, run: (password: string) => Promise<void>, byAccount = false) => {
      if (byAccount) {
        void run("");
        return;
      }
      if (password) {
        void run(password).then(() => {
          if (!keep) setPassword("");
        });
        return;
      }
      setAsking({
        why,
        go: (typed: string) => {
          setAsking(null);
          if (keep) setPassword(typed);
          void run(typed);
        },
      });
    },
    [password, keep],
  );

  const machines = data?.machines ?? [];

  const act = async (m: Machine, what: "shutdown" | "reboot", secret: string) => {
    setFailed(null);
    try {
      await api(`/api/projects/${project.id}/machines/${encodeURIComponent(m.name)}/power`, {
        body: { what, password: secret },
      });
      // It takes a moment to go down; the list says so on its own soon enough.
      setTimeout(reload, 4000);
    } catch (err) {
      setFailed(err as Error);
    }
  };

  return (
    <div>
      <div className="machines-bar">
        <label className="check" style={{ margin: 0 }}>
          <input type="checkbox" checked={keep} onChange={(e) => {
            setKeep(e.target.checked);
            if (!e.target.checked) setPassword("");
          }} />
          <span>Keep the SSH password while this page is open</span>
        </label>
        {password ? (
          <button className="btn small ghost" onClick={() => setPassword("")}>
            <Icon name="lock" size={13} /> Forget it now
          </button>
        ) : (
          <span className="meta">asked for whenever it is needed</span>
        )}
        <span className="grow" />
        <button className="btn small" onClick={reload}>
          <Icon name="refresh" size={14} /> Check again
        </button>
        {!project.readOnly ? (
          <button className="btn small" onClick={() => setEditing(machines.map((m) => ({ ...m })))}>
            <Icon name="settings" size={14} /> Machines
          </button>
        ) : null}
      </div>

      <ErrorBox error={failed ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data && machines.length === 0 ? (
        <Empty icon="server">
          No machines yet. Add one and it can be woken, shut down and looked into.
        </Empty>
      ) : null}

      <div className="tiles">
        {machines.map((m) => (
          <div key={m.name} className="tile machine">
            <div className="tile-top">
              <span className={m.up ? "dot-status on" : "dot-status off"} />
              <div style={{ minWidth: 0, flex: 1 }}>
                <h3>{m.name}</h3>
                <div className="sub mono">
                  {m.user ? `${m.user}@` : ""}
                  {m.host}
                  {m.port && m.port !== 22 ? `:${m.port}` : ""}
                </div>
              </div>
              <span className="meta">{m.up ? "up" : "not answering"}</span>
            </div>
            {m.note ? <p className="meta">{m.note}</p> : null}
            {m.account ? <p className="meta">signs in with the account “{m.account}”</p> : null}
            <div className="tile-foot">
              {!m.up && m.mac ? (
                <button
                  className="btn small primary"
                  onClick={async () => {
                    setFailed(null);
                    try {
                      await api(`/api/projects/${project.id}/machines/${encodeURIComponent(m.name)}/wake`, {
                        body: {},
                      });
                      setTimeout(reload, 8000);
                    } catch (err) {
                      setFailed(err as Error);
                    }
                  }}
                >
                  <Icon name="zap" size={14} /> Wake
                </button>
              ) : null}
              {m.up ? (
                <>
                  <button className="btn small" onClick={() => setOpen(open === m.name ? null : m.name)}>
                    <Icon name="code" size={14} /> Sessions
                  </button>
                  <button
                    className="btn small ghost"
                    onClick={() =>
                      withPassword(
                        `shutting ${m.name} down`,
                        (secret) => act(m, "shutdown", secret),
                        Boolean(m.account),
                      )
                    }
                  >
                    Shut down
                  </button>
                  <button
                    className="btn small ghost"
                    onClick={() =>
                      withPassword(
                        `restarting ${m.name}`,
                        (secret) => act(m, "reboot", secret),
                        Boolean(m.account),
                      )
                    }
                  >
                    Restart
                  </button>
                </>
              ) : null}
            </div>

            {open === m.name ? (
              <Tmux
                project={project}
                machine={m}
                withPassword={(why, run) => withPassword(why, run, Boolean(m.account))}
              />
            ) : null}
          </div>
        ))}
      </div>

      {asking ? (
        <AskPassword
          why={asking.why}
          keep={keep}
          onKeep={setKeep}
          onClose={() => setAsking(null)}
          onGo={asking.go}
        />
      ) : null}

      {editing ? (
        <EditMachines
          project={project}
          machines={editing}
          accounts={(accounts.data?.accounts ?? []).filter(
            (a) => a.kind === "machine" || a.kind === "ssh",
          )}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * The sessions on one machine, and what is on their screens.
 *
 * This is not a terminal emulator and does not pretend to be one: it is what
 * tmux itself shows — the pane as text — plus a line to type into. That covers
 * what these sessions are for: something long-running is in there, and you want
 * to look at it and occasionally tell it something.
 */
function Tmux({
  project,
  machine,
  withPassword,
}: {
  project: Project;
  machine: Machine;
  withPassword: (why: string, run: (password: string) => Promise<void>) => void;
}) {
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const [note, setNote] = useState("");
  const [inside, setInside] = useState<string | null>(null);
  const [screen, setScreen] = useState("");
  const [line, setLine] = useState("");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<Error | null>(null);
  const [live, setLive] = useState(true);
  const secret = useRef("");
  const base = `/api/projects/${project.id}/machines/${encodeURIComponent(machine.name)}`;

  const list = useCallback(
    (password: string) =>
      api<{ sessions: Session[]; note?: string }>(`${base}/tmux`, { body: { password } })
        .then((answer) => {
          secret.current = password;
          setSessions(answer.sessions);
          setNote(answer.note ?? "");
          setFailed(null);
        })
        .catch((err) => setFailed(err as Error)),
    [base],
  );

  const look = useCallback(
    (session: string, password: string) =>
      api<{ screen: string }>(`${base}/tmux/${encodeURIComponent(session)}`, {
        body: { password, lines: 200 },
      })
        .then((answer) => {
          secret.current = password;
          setScreen(answer.screen);
          setFailed(null);
        })
        .catch((err) => setFailed(err as Error)),
    [base],
  );

  useEffect(() => {
    withPassword(`the sessions on ${machine.name}`, list);
    // Asking once when it opens; the refresh below keeps it current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // While a session is open, its screen is fetched again every few seconds —
  // that is what makes it feel like looking at the thing rather than at a
  // photograph of it.
  useEffect(() => {
    if (!inside || !live || !secret.current) return;
    const timer = setInterval(() => void look(inside, secret.current), 4000);
    return () => clearInterval(timer);
  }, [inside, live, look]);

  return (
    <div className="tmux">
      <ErrorBox error={failed} />
      {sessions === null ? <Spinner /> : null}

      {sessions !== null && !inside ? (
        <>
          {sessions.length === 0 ? (
            <p className="meta">{note || "No tmux sessions on this machine."}</p>
          ) : (
            <div className="list">
              {sessions.map((s) => (
                <button
                  key={s.name}
                  className="list-row"
                  style={{ width: "100%", background: "none", border: "none", cursor: "pointer" }}
                  onClick={() => {
                    setInside(s.name);
                    withPassword(`the session ${s.name}`, (password) => look(s.name, password));
                  }}
                >
                  <Icon name="code" size={15} />
                  <span className="grow">{s.name}</span>
                  <span className="meta">
                    {s.windows} window{s.windows === "1" ? "" : "s"}
                    {s.attached ? " · attached" : ""}
                  </span>
                  <Icon name="chevronRight" size={14} />
                </button>
              ))}
            </div>
          )}
          <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
            <button
              className="btn small"
              onClick={() => withPassword(`the sessions on ${machine.name}`, list)}
            >
              <Icon name="refresh" size={13} /> Refresh
            </button>
            <button
              className="btn small"
              onClick={() => {
                const name = prompt("A name for the new session:");
                if (!name) return;
                withPassword(`a new session on ${machine.name}`, async (password) => {
                  setBusy(true);
                  try {
                    await api(`${base}/tmux-new`, { body: { password, session: name } });
                    await list(password);
                  } catch (err) {
                    setFailed(err as Error);
                  } finally {
                    setBusy(false);
                  }
                });
              }}
            >
              <Icon name="plus" size={13} /> New session
            </button>
          </div>
        </>
      ) : null}

      {inside ? (
        <>
          <div className="tmux-bar">
            <button className="btn ghost icon" aria-label="Back" onClick={() => setInside(null)}>
              <Icon name="chevronLeft" size={15} />
            </button>
            <strong className="mono">{inside}</strong>
            <label className="check" style={{ margin: 0 }}>
              <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
              <span className="meta">keep looking</span>
            </label>
            <span className="grow" />
            <button
              className="btn small ghost"
              onClick={() => withPassword(`the session ${inside}`, (p) => look(inside, p))}
            >
              <Icon name="refresh" size={13} /> Now
            </button>
            <button
              className="btn small ghost"
              onClick={() => {
                if (!confirm(`Close the session "${inside}"? Whatever runs in it stops.`)) return;
                withPassword(`closing ${inside}`, async (password) => {
                  await api(`${base}/tmux/${encodeURIComponent(inside)}/kill`, { body: { password } });
                  setInside(null);
                  await list(password);
                });
              }}
            >
              Close it
            </button>
          </div>

          <pre className="tmux-screen">{screen || " "}</pre>

          <form
            className="tmux-type"
            onSubmit={(e) => {
              e.preventDefault();
              const keys = line;
              setLine("");
              withPassword(`typing into ${inside}`, async (password) => {
                setBusy(true);
                try {
                  const answer = await api<{ screen: string }>(
                    `${base}/tmux/${encodeURIComponent(inside)}/keys`,
                    { body: { password, keys, enter: true } },
                  );
                  secret.current = password;
                  setScreen(answer.screen);
                } catch (err) {
                  setFailed(err as Error);
                } finally {
                  setBusy(false);
                }
              });
            }}
          >
            <span className="mono meta">$</span>
            <input
              value={line}
              onChange={(e) => setLine(e.target.value)}
              placeholder="a line for this session"
              className="mono"
            />
            <button className="btn small" disabled={busy || !line.trim()}>
              Send
            </button>
            <button
              type="button"
              className="btn small ghost"
              title="Ctrl-C"
              onClick={() =>
                withPassword(`interrupting ${inside}`, async (password) => {
                  const answer = await api<{ screen: string }>(
                    `${base}/tmux/${encodeURIComponent(inside)}/keys`,
                    { body: { password, keys: "C-c", enter: false } },
                  );
                  setScreen(answer.screen);
                })
              }
            >
              ^C
            </button>
          </form>
        </>
      ) : null}
    </div>
  );
}

function AskPassword({
  why,
  keep,
  onKeep,
  onClose,
  onGo,
}: {
  why: string;
  keep: boolean;
  onKeep: (keep: boolean) => void;
  onClose: () => void;
  onGo: (password: string) => void;
}) {
  const [typed, setTyped] = useState("");
  return (
    <Modal
      title="The machine's password"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={!typed} onClick={() => onGo(typed)}>
            Go on
          </button>
        </>
      }
    >
      <p className="meta" style={{ marginTop: 0 }}>
        For {why}. This is the SSH password of that machine — not this server's. It is used for one
        connection and kept nowhere on the server.
      </p>
      <Field label="Password">
        <input
          type="password"
          value={typed}
          autoFocus
          onChange={(e) => setTyped(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && typed) onGo(typed);
          }}
        />
      </Field>
      <label className="check">
        <input type="checkbox" checked={keep} onChange={(e) => onKeep(e.target.checked)} />
        <span>Keep it while this page is open, instead of asking every time</span>
      </label>
    </Modal>
  );
}

function EditMachines({
  project,
  machines,
  accounts,
  onClose,
  onSaved,
}: {
  project: Project;
  machines: Machine[];
  accounts: AccountRow[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [rows, setRows] = useState(machines);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (i: number, patch: Partial<Machine>) =>
    setRows(rows.map((row, j) => (i === j ? { ...row, ...patch } : row)));

  return (
    <Modal
      title="Machines"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/projects/${project.id}/machines`, {
                  method: "PUT",
                  body: {
                    machines: rows.map((m) => ({
                      name: m.name,
                      host: m.host,
                      port: Number(m.port) || undefined,
                      user: m.user,
                      mac: m.mac,
                      broadcast: m.broadcast,
                      account: m.account,
                      note: m.note,
                    })),
                  },
                });
                onSaved();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Save
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <p className="meta" style={{ marginTop: 0 }}>
        This is machines.json — a file in the project, so it travels with it. No password is in it.
      </p>
      {rows.map((m, i) => (
        <div key={i} className="machine-row">
          <div className="row">
            <Field label="Name">
              <input value={m.name} onChange={(e) => set(i, { name: e.target.value })} />
            </Field>
            <Field label="Address" hint="A name or an IP.">
              <input value={m.host} onChange={(e) => set(i, { host: e.target.value })} />
            </Field>
            <Field label="Port">
              <input
                type="number"
                value={m.port ?? ""}
                placeholder="22"
                onChange={(e) => set(i, { port: Number(e.target.value) || undefined })}
              />
            </Field>
          </div>
          <div className="row">
            <Field label="User" hint="Who to sign in as over SSH.">
              <input value={m.user ?? ""} onChange={(e) => set(i, { user: e.target.value })} />
            </Field>
            <Field label="MAC" hint="Needed to wake it, and only for that.">
              <input
                value={m.mac ?? ""}
                placeholder="aa:bb:cc:dd:ee:ff"
                onChange={(e) => set(i, { mac: e.target.value })}
              />
            </Field>
            <Field label="Broadcast" hint="Empty is the network's own.">
              <input
                value={m.broadcast ?? ""}
                placeholder="192.168.178.255"
                onChange={(e) => set(i, { broadcast: e.target.value })}
              />
            </Field>
          </div>
          <div className="row">
            <Field
              label="Account"
              hint="Add the machine once under Accounts and it is never asked for a password here."
            >
              <select value={m.account ?? ""} onChange={(e) => set(i, { account: e.target.value })}>
                <option value="">— type the password each time —</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.title}>
                    {a.title}
                    {a.needsSecret ? " (needs its password again)" : ""}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <div className="row" style={{ alignItems: "end" }}>
            <Field label="Note">
              <input value={m.note ?? ""} onChange={(e) => set(i, { note: e.target.value })} />
            </Field>
            <button className="btn small ghost" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
              <Icon name="trash" size={14} /> Remove
            </button>
          </div>
        </div>
      ))}
      <button
        className="btn small"
        onClick={() => setRows([...rows, { name: "", host: "", up: false } as Machine])}
      >
        <Icon name="plus" size={14} /> Another machine
      </button>
    </Modal>
  );
}
