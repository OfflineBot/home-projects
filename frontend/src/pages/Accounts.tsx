import { useState } from "react";
import { Icon } from "../components/Icon";
import { Fields } from "../components/Fields";
import { Empty, ErrorBox, Field, formatDate, Modal, Spinner, useAsk, useGuarded } from "../components/ui";
import NewAccount from "../components/NewAccount";
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

/**
 * A list of lines, with a button.
 *
 * Some things are a list — the addresses of the lamps in a room, for instance —
 * and a box you type commas into is a list pretending to be a sentence. One
 * line each, a cross to take one away, a button to add another.
 */

export default function Accounts() {
  const ask = useAsk();
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
    const sure = await ask.confirm({
      title: `Test “${account.title}”?`,
      confirmLabel: "Test it",
      body: <>Same as a real run: a failed sign-in costs the stored password.</>,
    });
    if (!sure) return;
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
                    const sure = await ask.confirm({
                      title: `Delete “${a.title}”?`,
                      confirmLabel: "Delete",
                      danger: true,
                      body: <>Its schedulers are paused and named, not deleted.</>,
                    });
                    if (!sure) return;
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
        <NewAccount
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
  // Kept as it is: a list of addresses is a list, and turning every value into
  // a string on the way in is how it became one long line again.
  const [config, setConfig] = useState<Record<string, any>>({ ...(account.config ?? {}) });
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

      {/* One renderer for every setting there is — see components/Fields. */}
      <Fields specs={kind?.fields ?? []} values={config} onChange={setConfig} />

      <p className="meta">The password is not shown and is not touched by this.</p>
    </Modal>
  );
}
