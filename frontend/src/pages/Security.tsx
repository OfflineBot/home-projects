import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate, useGuarded } from "../components/ui";
import { api, type Token } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

interface Session {
  id: string;
  userAgent: string;
  ip: string;
  createdAt: string;
  lastUsedAt: string;
  current: boolean;
}

/** Sessions, the second factor, tokens for machines, and the log. */
export default function Security() {
  const session = useSession();
  const guarded = useGuarded();
  const sessions = useQuery<{ sessions: Session[] }>("/api/auth/sessions");
  const tokens = useQuery<{ tokens: Token[] }>("/api/tokens");
  const audit = useQuery<{ entries: { id: number; action: string; subject: string; ip: string; createdAt: string }[] }>(
    "/api/auth/audit",
  );
  const sshKeys = useQuery<{
    keys: { id: string; name: string; fingerprint: string; lastUsedAt?: string }[];
    enabled: boolean;
    host: string;
    note?: string;
  }>("/api/ssh-keys");
  const [totp, setTotp] = useState<{ secret: string; url: string } | null>(null);
  const [code, setCode] = useState("");
  const [newToken, setNewToken] = useState<Token | null>(null);
  const [creatingToken, setCreatingToken] = useState(false);
  const [addingKey, setAddingKey] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  if (!session.user) return <Empty icon="lock">Sign in first.</Empty>;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Security</h1>
          <p>Who is signed in, what may reach the server, and what happened.</p>
        </div>
      </div>

      <ErrorBox error={error} />

      <h2 style={{ fontSize: 17 }}>Sessions</h2>
      {sessions.loading && !sessions.data ? <Spinner /> : null}
      <div className="list">
        {sessions.data?.sessions.map((s) => (
          <div key={s.id} className="list-row">
            <Icon name="users" size={16} />
            <span className="grow">
              {s.userAgent || "unknown device"}
              {s.current ? <span className="badge good" style={{ marginLeft: 8 }}>this one</span> : null}
            </span>
            <span className="meta">{s.ip}</span>
            <span className="meta">{formatDate(s.lastUsedAt)}</span>
            <button
              className="btn small"
              onClick={async () => {
                await api(`/api/auth/sessions/${s.id}`, { method: "DELETE" });
                sessions.reload();
              }}
            >
              Revoke
            </button>
          </div>
        ))}
      </div>
      <button
        className="btn"
        style={{ marginTop: 10 }}
        onClick={async () => {
          await api("/api/auth/sessions/revoke-all", { method: "POST" });
          sessions.reload();
        }}
      >
        Sign out everywhere else
      </button>

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Second factor</h2>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        Optional. {session.user.totpEnabled ? "It is on." : "It is off."}
      </p>
      {session.user.totpEnabled ? (
        <button
          className="btn danger"
          onClick={async () => {
            try {
              await guarded("switching the second factor off", () =>
                api("/api/auth/totp/disable", { method: "POST" }),
              );
              location.reload();
            } catch (err) {
              setError(err as Error);
            }
          }}
        >
          Switch off
        </button>
      ) : (
        <button
          className="btn"
          onClick={async () => {
            try {
              const res = await guarded("switching the second factor on", () =>
                api<{ secret: string; url: string }>("/api/auth/totp/start", { method: "POST" }),
              );
              setTotp(res);
            } catch (err) {
              setError(err as Error);
            }
          }}
        >
          Set up
        </button>
      )}

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Tokens for machines</h2>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        For the things without a login screen. Each one is shown once, is scoped to a project or a group, and can
        be revoked on its own.
      </p>
      <button className="btn" style={{ marginBottom: 10 }} onClick={() => setCreatingToken(true)}>
        <Icon name="plus" size={15} /> New token
      </button>
      <div className="list">
        {tokens.data?.tokens.map((t) => (
          <div key={t.id} className="list-row">
            <Icon name="key" size={16} />
            <span className="grow">{t.name}</span>
            <span className="badge">{t.scope}</span>
            <span className="meta">{t.lastUsedAt ? `last used ${formatDate(t.lastUsedAt)}` : "never used"}</span>
            <button
              className="btn small danger"
              onClick={async () => {
                await api(`/api/tokens/${t.id}`, { method: "DELETE" });
                tokens.reload();
              }}
            >
              Revoke
            </button>
          </div>
        ))}
        {tokens.data && tokens.data.tokens.length === 0 ? (
          <div className="list-row">
            <span className="meta">none yet</span>
          </div>
        ) : null}
      </div>

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Keys for git over SSH</h2>
      {sshKeys.data?.enabled ? (
        <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
          A registered key can clone and push over{" "}
          <code className="mono">{sshKeys.data.host}:&lt;group&gt;.git</code> — and nothing else on that
          machine: no shell, no forwarding. A push over SSH goes through the same checks as one over HTTPS.
        </p>
      ) : (
        <div className="warning">{sshKeys.data?.note ?? "Git over SSH is not set up on this server."}</div>
      )}
      <button className="btn" style={{ marginBottom: 10 }} onClick={() => setAddingKey(true)}>
        <Icon name="plus" size={15} /> Add a key
      </button>
      <div className="list">
        {sshKeys.data?.keys.map((k) => (
          <div key={k.id} className="list-row">
            <Icon name="key" size={16} />
            <span className="grow">{k.name}</span>
            <code className="mono meta">{k.fingerprint}</code>
            <span className="meta">{k.lastUsedAt ? `last used ${formatDate(k.lastUsedAt)}` : "never used"}</span>
            <button
              className="btn small danger"
              onClick={async () => {
                await api(`/api/ssh-keys/${k.id}`, { method: "DELETE" });
                sshKeys.reload();
              }}
            >
              Remove
            </button>
          </div>
        ))}
        {sshKeys.data && sshKeys.data.keys.length === 0 ? (
          <div className="list-row">
            <span className="meta">none yet</span>
          </div>
        ) : null}
      </div>

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Log</h2>
      <div className="list">
        {audit.data?.entries.slice(0, 40).map((e) => (
          <div key={e.id} className="list-row">
            <span className="badge">{e.action}</span>
            <span className="grow">{e.subject}</span>
            <span className="meta">{e.ip}</span>
            <span className="meta">{formatDate(e.createdAt)}</span>
          </div>
        ))}
      </div>

      {totp ? (
        <Modal
          title="Second factor"
          onClose={() => setTotp(null)}
          footer={
            <>
              <button className="btn" onClick={() => setTotp(null)}>Cancel</button>
              <button
                className="btn primary"
                onClick={async () => {
                  try {
                    await api("/api/auth/totp/enable", { method: "POST", body: { code } });
                    setTotp(null);
                    location.reload();
                  } catch (err) {
                    setError(err as Error);
                  }
                }}
              >
                Switch on
              </button>
            </>
          }
        >
          <p style={{ marginTop: 0 }}>Add this to your authenticator, then confirm with a code.</p>
          <Field label="Secret">
            <input readOnly value={totp.secret} onFocus={(e) => e.currentTarget.select()} />
          </Field>
          <Field label="otpauth URL">
            <input readOnly value={totp.url} onFocus={(e) => e.currentTarget.select()} />
          </Field>
          <Field label="Code">
            <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoFocus />
          </Field>
        </Modal>
      ) : null}

      {addingKey ? (
        <AddSSHKey
          onClose={() => setAddingKey(false)}
          onAdded={() => {
            setAddingKey(false);
            sshKeys.reload();
          }}
        />
      ) : null}

      {creatingToken ? (
        <CreateToken
          onClose={() => setCreatingToken(false)}
          onCreated={(t) => {
            setCreatingToken(false);
            setNewToken(t);
            tokens.reload();
          }}
        />
      ) : null}

      {newToken ? (
        <Modal title="Your token" onClose={() => setNewToken(null)}>
          <p style={{ marginTop: 0 }}>This is the only time it is shown.</p>
          <input readOnly value={newToken.secret} onFocus={(e) => e.currentTarget.select()} />
        </Modal>
      ) : null}
    </>
  );
}

function CreateToken({ onClose, onCreated }: { onClose: () => void; onCreated: (t: Token) => void }) {
  const guarded = useGuarded();
  const projects = useQuery<{ projects: { id: string; title: string; groupSlug?: string }[] }>("/api/projects");
  const [name, setName] = useState("");
  const [scope, setScope] = useState("read");
  const [projectId, setProjectId] = useState("");
  const [error, setError] = useState<Error | null>(null);

  return (
    <Modal
      title="New token"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={!name.trim() || !projectId}
            onClick={async () => {
              try {
                const token = await guarded("creating a token", () =>
                  api<Token>("/api/tokens", { body: { name, scope, projectId } }),
                );
                onCreated(token);
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Create
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <p style={{ marginTop: 0, color: "var(--ctp-subtext0)" }}>
        A token is scoped to one project and one permission, is shown once, and can be revoked on its own.
      </p>
      <Field label="What it is for">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="the phone's calendar" autoFocus />
      </Field>
      <Field label="Project">
        <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
          <option value="">— pick one —</option>
          {(projects.data?.projects ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.groupSlug ? `${p.groupSlug} / ` : ""}
              {p.title}
            </option>
          ))}
        </select>
      </Field>
      <Field label="May">
        <select value={scope} onChange={(e) => setScope(e.target.value)}>
          <option value="read">read</option>
          <option value="write">read and write</option>
          <option value="ics">fetch the calendar</option>
          <option value="git">clone and push</option>
          <option value="webhook">trigger a webhook</option>
        </select>
      </Field>
    </Modal>
  );
}

function AddSSHKey({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const guarded = useGuarded();
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [error, setError] = useState<Error | null>(null);

  return (
    <Modal
      title="Add a key"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={!key.trim()}
            onClick={async () => {
              try {
                await guarded("adding a key that may reach the repositories", () =>
                  api("/api/ssh-keys", { body: { name, key } }),
                );
                onAdded();
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Add
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <p style={{ marginTop: 0, color: "var(--ctp-subtext0)" }}>
        The <strong>public</strong> key — the file ending in <code className="mono">.pub</code>. On your
        machine: <code className="mono">cat ~/.ssh/id_ed25519.pub</code>. If you have none yet:{" "}
        <code className="mono">ssh-keygen -t ed25519</code>.
      </p>
      <Field label="What machine is this">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="laptop" autoFocus />
      </Field>
      <Field label="Public key">
        <textarea value={key} onChange={(e) => setKey(e.target.value)} placeholder="ssh-ed25519 AAAA… you@laptop" />
      </Field>
    </Modal>
  );
}
