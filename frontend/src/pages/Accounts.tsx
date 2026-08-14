import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate, useGuarded } from "../components/ui";
import { api, type Account, type AccountKind } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/**
 * Credentials live here and nowhere else — never in a project, because a
 * project gets versioned, linked, published and cloned.
 *
 * The rule this page is built around: a password is used once. Any attempt
 * that does not end in a confirmed sign-in deletes it, and nothing tries
 * again on its own.
 */
export default function Accounts() {
  const session = useSession();
  const guarded = useGuarded();
  const { data, error, loading, reload } = useQuery<{ accounts: Account[]; kinds: AccountKind[] }>("/api/accounts");
  const [creating, setCreating] = useState(false);
  const [entering, setEntering] = useState<Account | null>(null);
  const [editing, setEditing] = useState<Account | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);

  if (!session.user) return <Empty icon="lock">Sign in to see the accounts.</Empty>;

  const kindOf = (name: string) => data?.kinds.find((k) => k.name === name);

  const test = async (account: Account) => {
    if (
      !confirm(
        `“Test connection” is the same attempt as a real run, with the same consequences: if it does not end ` +
          `in a confirmed sign-in, the stored password is deleted and has to be entered again.\n\nRun it for ` +
          `“${account.title}”?`,
      )
    )
      return;
    setTesting(account.id);
    setResult(null);
    setActionError(null);
    try {
      await guarded("testing an account", () => api(`/api/accounts/${account.id}/test`, { method: "POST" }));
      setResult({ ok: true, message: `${account.title}: signed in.` });
    } catch (err) {
      setResult({ ok: false, message: (err as Error).message });
    } finally {
      setTesting(null);
      reload();
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Accounts</h1>
        </div>
        <div className="head-actions">
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Icon name="plus" size={16} /> New account
          </button>
        </div>
      </div>

      <div className="warning">
        <strong>Single-use.</strong> Only a confirmed sign-in keeps a password; anything else deletes it and
        pauses the schedulers that used it.
      </div>

      <ErrorBox error={actionError ?? error} onRetry={reload} />
      {result ? <div className={result.ok ? "notice" : "error"}>{result.message}</div> : null}
      {loading && !data ? <Spinner /> : null}

      {data && data.accounts.length === 0 ? <Empty icon="key">No account yet.</Empty> : null}

      <div className="tiles">
        {data?.accounts.map((a) => {
          const kind = kindOf(a.kind);
          return (
            <div key={a.id} className="tile">
              <div className="tile-top">
                <span className="tile-icon">
                  <Icon name="key" />
                </span>
                <div style={{ minWidth: 0 }}>
                  <h3>{a.title}</h3>
                  <div className="sub">{kind?.title ?? a.kind}</div>
                </div>
              </div>

              {a.needsSecret ? (
                <div className="error" style={{ margin: 0 }}>
                  <Icon name="alert" size={16} />
                  <div>
                    <strong>Enter the password again.</strong>
                    <div>{a.lastError || "The stored one was used up."}</div>
                    {a.consumedAt ? <div className="meta">used up {formatDate(a.consumedAt)}</div> : null}
                  </div>
                </div>
              ) : (
                <div className="sub">
                  {a.lastOkAt ? `last success ${formatDate(a.lastOkAt)}` : "not used yet"}
                  {a.schedulerCount ? ` · ${a.schedulerCount} scheduler(s)` : ""}
                </div>
              )}

              <div className="tile-foot">
                <span className={`badge ${a.state === "ok" ? "good" : a.needsSecret ? "bad" : ""}`}>{a.state}</span>
                {a.attemptInFlight ? <span className="badge warn">attempt in flight</span> : null}
                <div style={{ flex: 1 }} />
                <button className="btn small" onClick={() => setEditing(a)}>
                  <Icon name="settings" size={13} /> Edit
                </button>
                <button className="btn small" onClick={() => setEntering(a)}>
                  <Icon name="key" size={13} /> {a.needsSecret ? "Enter password" : "Replace password"}
                </button>
                {kind?.secretLabel ? (
                  <button className="btn small" disabled={testing === a.id || a.needsSecret} onClick={() => test(a)}>
                    {testing === a.id ? "…" : "Test connection"}
                  </button>
                ) : null}
                <button
                  className="btn small danger"
                  onClick={async () => {
                    if (!confirm(`Delete “${a.title}”? Its schedulers are paused and named, not deleted.`)) return;
                    try {
                      const res = await guarded("deleting an account", () =>
                        api<{ pausedSchedulers: string[] }>(`/api/accounts/${a.id}`, { method: "DELETE" }),
                      );
                      if (res.pausedSchedulers?.length) {
                        setResult({ ok: true, message: `Paused: ${res.pausedSchedulers.join(", ")}` });
                      }
                      reload();
                    } catch (err) {
                      setActionError(err as Error);
                    }
                  }}
                >
                  <Icon name="trash" size={13} />
                </button>
              </div>
            </div>
          );
        })}
      </div>

      {creating && data ? (
        <CreateAccount
          kinds={data.kinds}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            reload();
          }}
        />
      ) : null}

      {editing ? (
        <EditAccount
          account={editing}
          kind={kindOf(editing.kind)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      ) : null}

      {entering ? (
        <EnterSecret
          account={entering}
          kind={kindOf(entering.kind)}
          onClose={() => setEntering(null)}
          onSaved={() => {
            setEntering(null);
            reload();
          }}
        />
      ) : null}
    </>
  );
}

function CreateAccount({
  kinds,
  onClose,
  onCreated,
}: {
  kinds: AccountKind[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const guarded = useGuarded();
  const [kindName, setKindName] = useState(kinds[0]?.name ?? "");
  const [title, setTitle] = useState("");
  const [config, setConfig] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState("");
  const [provider, setProvider] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const kind = kinds.find((k) => k.name === kindName);

  return (
    <Modal
      title="New account"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            onClick={async () => {
              try {
                await guarded("storing credentials", () =>
                  api("/api/accounts", { body: { kind: kindName, title, config, secret } }),
                );
                onCreated();
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Save
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Kind">
        <select value={kindName} onChange={(e) => setKindName(e.target.value)}>
          {kinds.map((k) => (
            <option key={k.name} value={k.name}>
              {k.title}
            </option>
          ))}
        </select>
      </Field>
      {kind?.description ? <p className="hint">{kind.description}</p> : null}
      {kind?.providers?.length ? (
        <Field label="Provider" hint="Fills in the servers and ports. Only the user name is left.">
          <select
            value={provider}
            onChange={(e) => {
              const p = kind.providers?.find((x) => x.name === e.target.value);
              setProvider(e.target.value);
              if (p) {
                setConfig({ ...config, ...p.fields });
                if (!title.trim()) setTitle(p.title);
              }
            }}
          >
            <option value="">Somewhere else</option>
            {kind.providers.map((p) => (
              <option key={p.name} value={p.name}>
                {p.title}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      {kind?.providers?.find((p) => p.name === provider)?.note ? (
        <p className="hint">{kind.providers.find((p) => p.name === provider)?.note}</p>
      ) : null}
      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={kind?.title} />
      </Field>
      {kind?.fields.map((f) => (
        <Field key={f.name} label={f.label} hint={f.hint}>
          {f.options?.length ? (
            <select
              value={config[f.name] ?? String(f.default ?? f.options[0].value)}
              onChange={(e) => setConfig({ ...config, [f.name]: e.target.value })}
            >
              {f.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              type={f.type === "password" ? "password" : f.type === "number" ? "number" : "text"}
              placeholder={f.placeholder}
              value={config[f.name] ?? ""}
              onChange={(e) => setConfig({ ...config, [f.name]: e.target.value })}
            />
          )}
        </Field>
      ))}
      {kind?.secretLabel ? (
        <Field
          label={kind.secretLabel}
          hint={
            kind.secretIsKey
              ? "A key, not a password — a failed connection does not consume it."
              : "Single-use: a failed attempt deletes it and it has to be typed in again."
          }
        >
          {kind.secretIsKey ? (
            <textarea value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
          ) : (
            <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} />
          )}
        </Field>
      ) : null}
    </Modal>
  );
}

function EnterSecret({
  account,
  kind,
  onClose,
  onSaved,
}: {
  account: Account;
  kind?: AccountKind;
  onClose: () => void;
  onSaved: () => void;
}) {
  const guarded = useGuarded();
  const [secret, setSecret] = useState("");
  const [error, setError] = useState<Error | null>(null);

  return (
    <Modal
      title={`${account.title} · ${kind?.secretLabel ?? "Password"}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!secret}
            onClick={async () => {
              try {
                await guarded("entering a password", () =>
                  api(`/api/accounts/${account.id}/secret`, { method: "POST", body: { secret } }),
                );
                onSaved();
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Store
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <p className="meta" style={{ marginTop: 0 }}>
        The password of {kind?.title ?? account.kind} — not this server's.
      </p>
      <Field label={kind?.secretLabel ?? "Password"}>
        {kind?.secretIsKey ? (
          <textarea value={secret} onChange={(e) => setSecret(e.target.value)} autoFocus />
        ) : (
          <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoFocus />
        )}
      </Field>
    </Modal>
  );
}

/**
 * Everything about an account except the one thing that cannot be read back:
 * the password. Changing an address does not touch it — a typo in a URL should
 * not cost a credential.
 */
function EditAccount({
  account,
  kind,
  onClose,
  onSaved,
}: {
  account: Account;
  kind?: AccountKind;
  onClose: () => void;
  onSaved: () => void;
}) {
  const guarded = useGuarded();
  const [title, setTitle] = useState(account.title);
  const [config, setConfig] = useState<Record<string, string>>(
    Object.fromEntries(Object.entries(account.config ?? {}).map(([k, v]) => [k, String(v ?? "")])),
  );
  const [provider, setProvider] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <Modal
      title={account.title}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy || !title.trim()}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await guarded("changing an account", () =>
                  api(`/api/accounts/${account.id}`, { method: "PATCH", body: { title, config } }),
                );
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
      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
      </Field>
      {kind?.providers?.length ? (
        <Field label="Provider" hint="Fills in the servers and ports. Only the user name is left.">
          <select
            value={provider}
            onChange={(e) => {
              const p = kind.providers?.find((x) => x.name === e.target.value);
              setProvider(e.target.value);
              if (p) {
                setConfig({ ...config, ...p.fields });
                if (!title.trim()) setTitle(p.title);
              }
            }}
          >
            <option value="">Somewhere else</option>
            {kind.providers.map((p) => (
              <option key={p.name} value={p.name}>
                {p.title}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      {kind?.providers?.find((p) => p.name === provider)?.note ? (
        <p className="hint">{kind.providers.find((p) => p.name === provider)?.note}</p>
      ) : null}

      {(kind?.fields ?? []).map((f) => (
        <Field key={f.name} label={f.label} hint={f.hint}>
          {f.options?.length ? (
            <select
              value={config[f.name] ?? String(f.default ?? f.options[0].value)}
              onChange={(e) => setConfig({ ...config, [f.name]: e.target.value })}
            >
              {f.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              type={f.type === "password" ? "password" : f.type === "number" ? "number" : "text"}
              placeholder={f.placeholder}
              value={config[f.name] ?? ""}
              onChange={(e) => setConfig({ ...config, [f.name]: e.target.value })}
            />
          )}
        </Field>
      ))}
      <p className="meta">The password is not shown and is not touched by this.</p>
    </Modal>
  );
}
