import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, formatDate, Spinner, useAsk, useGuarded } from "../components/ui";
import { api } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/**
 * Who is on this server.
 *
 * Anyone can ask for an account; nothing happens until it is let in here. The
 * waiting ones come first, because they are the only entries that need a
 * decision — the rest is a list.
 */

interface Account {
  id: string;
  username: string;
  displayName: string;
  isOwner: boolean;
  approved: boolean;
  approvedAt?: string;
  createdAt?: string;
  note?: string;
  totpEnabled?: boolean;
}

export default function Users() {
  const ask = useAsk();
  const session = useSession();
  const guarded = useGuarded();
  const { data, error, loading, reload } = useQuery<{ users: Account[] }>("/api/users");
  const [busy, setBusy] = useState("");
  const [failed, setFailed] = useState<Error | null>(null);

  if (!session.user?.isOwner) {
    return <Empty icon="lock">This page is the owner's.</Empty>;
  }

  const act = async (id: string, what: string, run: () => Promise<unknown>) => {
    setBusy(id);
    setFailed(null);
    try {
      await guarded(what, run);
      reload();
    } catch (err) {
      setFailed(err as Error);
    } finally {
      setBusy("");
    }
  };

  const users = data?.users ?? [];
  const waiting = users.filter((u) => !u.approved);
  const inside = users.filter((u) => u.approved);

  return (
    <div>
      <h1>People</h1>
      <ErrorBox error={error || failed} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {waiting.length ? (
        <>
          <h2>Waiting</h2>
          <div className="list">
            {waiting.map((u) => (
              <div key={u.id} className="list-row">
                <Icon name="users" size={16} />
                <span className="grow">
                  <strong>{u.username}</strong>
                  {u.note ? <span className="meta"> · {u.note}</span> : null}
                  {u.createdAt ? <span className="meta"> · asked {formatDate(u.createdAt)}</span> : null}
                </span>
                <button
                  className="btn small primary"
                  disabled={busy === u.id}
                  onClick={() => act(u.id, "letting someone in", () => api(`/api/users/${u.id}/approve`, { body: {} }))}
                >
                  Let in
                </button>
                <button
                  className="btn small ghost"
                  disabled={busy === u.id}
                  onClick={() => act(u.id, "removing an account", () => api(`/api/users/${u.id}`, { method: "DELETE" }))}
                >
                  Turn away
                </button>
              </div>
            ))}
          </div>
        </>
      ) : null}

      <h2>Inside</h2>
      {inside.length === 0 && !loading ? <Empty icon="users">Nobody yet.</Empty> : null}
      <div className="list">
        {inside.map((u) => (
          <div key={u.id} className="list-row">
            <Icon name={u.isOwner ? "key" : "users"} size={16} />
            <span className="grow">
              <strong>{u.username}</strong>
              {u.isOwner ? <span className="badge">owner</span> : null}
              {u.totpEnabled ? <span className="badge">2FA</span> : null}
              {u.approvedAt ? <span className="meta"> · in since {formatDate(u.approvedAt)}</span> : null}
            </span>
            {u.isOwner ? null : (
              <>
                <button
                  className="btn small ghost"
                  disabled={busy === u.id}
                  onClick={() => act(u.id, "suspending an account", () => api(`/api/users/${u.id}/approve?undo=true`, { body: {} }))}
                >
                  Suspend
                </button>
                <button
                  className="btn small danger"
                  disabled={busy === u.id}
                  onClick={async () => {
                    const sure = await ask.confirm({
                      title: `Remove ${u.username}?`,
                      confirmLabel: "Remove",
                      danger: true,
                      body: <>Everything it made goes with it.</>,
                    });
                    if (sure) {
                      void act(u.id, "removing an account", () =>
                        api(`/api/users/${u.id}`, { method: "DELETE" }),
                      );
                    }
                  }}
                >
                  Remove
                </button>
              </>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
