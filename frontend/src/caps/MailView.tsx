import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, authedUrl, type Account, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/** The mailbox: every message is an .eml file in the project. */
export default function MailView({ project }: { project: Project; reload: () => void }) {
  const [params, setParams] = useSearchParams();
  const selected = params.get("mail");
  const { data, error, loading, reload: reloadList } = useQuery<{
    messages: { path: string; folder: string; from: string; subject: string; date: string }[];
  }>(`/api/projects/${project.id}/mail/messages`);
  const [composing, setComposing] = useState(false);

  const open = (path: string | null) => {
    const p = new URLSearchParams(params);
    if (path) p.set("mail", path);
    else p.delete("mail");
    setParams(p);
  };

  return (
    <div className="grid-2" style={{ gridTemplateColumns: "minmax(260px, 380px) 1fr", alignItems: "start" }}>
      <div>
        <div style={{ display: "flex", gap: 8, marginBottom: 10 }}>
          <strong style={{ flex: 1 }}>{data?.messages.length ?? 0} messages</strong>
          {!project.readOnly ? (
            <button className="btn small" onClick={() => setComposing(true)}>
              <Icon name="plus" size={14} /> Write
            </button>
          ) : null}
        </div>
        <ErrorBox error={error} onRetry={reloadList} />
        {loading && !data ? <Spinner /> : null}
        {data && data.messages.length === 0 ? (
          <Empty icon="mail">Nothing here. A mail scheduler fills this folder.</Empty>
        ) : null}
        <div className="list">
          {data?.messages.map((m) => (
            <button
              key={m.path}
              className="list-row"
              style={{
                background: m.path === selected ? "var(--ctp-surface0)" : undefined,
                border: "none",
                textAlign: "left",
                cursor: "pointer",
                width: "100%",
              }}
              onClick={() => open(m.path)}
            >
              <div className="grow">
                <div style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{m.subject || "(no subject)"}</div>
                <div className="meta">{m.from}</div>
              </div>
              <span className="meta">{formatDate(m.date, false)}</span>
            </button>
          ))}
        </div>
      </div>

      <div>{selected ? <Message project={project} path={selected} /> : <Empty icon="mail">Pick a message.</Empty>}</div>

      {composing ? <Compose project={project} onClose={() => setComposing(false)} onSent={reloadList} /> : null}
    </div>
  );
}

function Message({ project, path }: { project: Project; path: string }) {
  const { data, error, loading } = useQuery<{
    from: string;
    to: string;
    subject: string;
    date: string;
    text: string;
    html: string;
    raw?: string;
    error?: string;
  }>(`/api/projects/${project.id}/mail/message?path=${encodeURIComponent(path)}`);

  return (
    <div>
      <ErrorBox error={error} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <div className="tile">
          <h3 style={{ fontSize: 18 }}>{data.subject || "(no subject)"}</h3>
          <div className="sub">
            {data.from} → {data.to} · {formatDate(data.date)}
          </div>
          {data.error ? <div className="warning">{data.error}</div> : null}
          {data.text ? (
            <pre className="block" style={{ whiteSpace: "pre-wrap" }}>{data.text}</pre>
          ) : data.html ? (
            <div className="notice">
              This message has only an HTML part. It is not rendered here on purpose — open the .eml file for it.
            </div>
          ) : null}
          <a className="btn small" href={authedUrl(`/api/projects/${project.id}/files/download?path=${encodeURIComponent(path)}`)}>
            <Icon name="download" size={14} /> Download the .eml
          </a>
        </div>
      ) : null}
    </div>
  );
}

function Compose({ project, onClose, onSent }: { project: Project; onClose: () => void; onSent: () => void }) {
  const accounts = useQuery<{ accounts: Account[] }>("/api/accounts");
  const [form, setForm] = useState({ account: "", to: "", subject: "", body: "" });
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const mailAccounts = (accounts.data?.accounts ?? []).filter((a) => a.kind === "mail");

  return (
    <Modal
      title="Write a message"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy || !form.account || !form.to}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/projects/${project.id}/mail/send`, { method: "POST", body: form });
                onSent();
                onClose();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Send
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Through which account" hint="Credentials live in the accounts menu, never in a project.">
        <select value={form.account} onChange={(e) => setForm({ ...form, account: e.target.value })}>
          <option value="">— pick one —</option>
          {mailAccounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.title}
              {a.needsSecret ? " (needs its password again)" : ""}
            </option>
          ))}
        </select>
      </Field>
      <Field label="To">
        <input value={form.to} onChange={(e) => setForm({ ...form, to: e.target.value })} />
      </Field>
      <Field label="Subject">
        <input value={form.subject} onChange={(e) => setForm({ ...form, subject: e.target.value })} />
      </Field>
      <Field label="Message">
        <textarea value={form.body} onChange={(e) => setForm({ ...form, body: e.target.value })} />
      </Field>
    </Modal>
  );
}
