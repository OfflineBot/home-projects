import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Account, type Project, type Scheduler, type SchedulerKind, type SchedulerRun } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/** What pulls data in from outside, and what came of every run. */
export default function Schedulers() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<{ schedulers: Scheduler[]; kinds: SchedulerKind[] }>(
    "/api/schedulers",
  );
  const runs = useQuery<{ runs: SchedulerRun[] }>("/api/runs");
  const [creating, setCreating] = useState(false);
  const [showLog, setShowLog] = useState<SchedulerRun | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);

  if (!session.user) return <Empty icon="lock">Sign in to see the schedulers.</Empty>;

  const run = async (s: Scheduler) => {
    setBusy(s.id);
    setActionError(null);
    try {
      const result = await api<{ run: SchedulerRun }>(`/api/schedulers/${s.id}/run`, { method: "POST" });
      setShowLog(result.run);
    } catch (err) {
      setActionError(err as Error);
    } finally {
      setBusy(null);
      reload();
      runs.reload();
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Schedulers</h1>
          <p>Each one belongs to a project and writes into its files. Running one by hand is always possible.</p>
        </div>
        <div className="head-actions">
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Icon name="plus" size={16} /> New scheduler
          </button>
        </div>
      </div>

      <ErrorBox error={actionError ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data && data.schedulers.length === 0 ? <Empty icon="clock">Nothing scheduled yet.</Empty> : null}

      <div className="tiles">
        {data?.schedulers.map((s) => (
          <div key={s.id} className="tile">
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name="clock" />
              </span>
              <div style={{ minWidth: 0 }}>
                <h3>{s.title || s.kind}</h3>
                <div className="sub">
                  {s.kind} → {s.projectSlug}
                  {s.targetPath ? `/${s.targetPath}` : ""}
                </div>
              </div>
            </div>
            <div className="sub">
              {s.schedule === "manual" ? "by hand only" : s.schedule}
              {s.nextRun ? ` · next ${formatDate(s.nextRun)}` : ""}
              {s.accountName ? ` · ${s.accountName}` : ""}
            </div>
            {s.pausedReason ? <div className="warning" style={{ margin: 0 }}>{s.pausedReason}</div> : null}
            <div className="tile-foot">
              <span className={`badge ${s.lastStatus === "ok" ? "good" : s.lastStatus === "error" ? "bad" : ""}`}>
                {s.lastStatus || "never run"}
              </span>
              {!s.enabled ? <span className="badge warn">paused</span> : null}
              <div style={{ flex: 1 }} />
              <button className="btn small primary" disabled={busy === s.id} onClick={() => run(s)}>
                <Icon name="play" size={13} /> Run now
              </button>
              <button
                className="btn small"
                onClick={async () => {
                  await api(`/api/schedulers/${s.id}`, { method: "PATCH", body: { enabled: !s.enabled } });
                  reload();
                }}
              >
                {s.enabled ? "Pause" : "Resume"}
              </button>
              <button
                className="btn small danger"
                onClick={async () => {
                  if (!confirm("Delete this scheduler?")) return;
                  await api(`/api/schedulers/${s.id}`, { method: "DELETE" });
                  reload();
                }}
              >
                <Icon name="trash" size={13} />
              </button>
            </div>
          </div>
        ))}
      </div>

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Runs</h2>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        Every run is logged. A failed one is visible here, not silent.
      </p>
      {runs.data?.runs.length ? (
        <div className="list">
          {runs.data.runs.map((r) => (
            <button
              key={r.id}
              className="list-row"
              style={{ border: "none", background: "none", textAlign: "left", cursor: "pointer", width: "100%" }}
              onClick={() => setShowLog(r)}
            >
              <span className={`badge ${r.status === "ok" ? "good" : "bad"}`}>{r.status}</span>
              <span className="grow">{r.message || "—"}</span>
              <span className="meta">{r.trigger}</span>
              <span className="meta">{formatDate(r.startedAt)}</span>
            </button>
          ))}
        </div>
      ) : (
        <p style={{ color: "var(--ctp-subtext0)" }}>Nothing has run yet.</p>
      )}

      {showLog ? (
        <Modal title={`Run · ${showLog.status}`} onClose={() => setShowLog(null)}>
          <p style={{ marginTop: 0 }}>{showLog.message}</p>
          <div className="sub">
            {formatDate(showLog.startedAt)} · {showLog.filesChanged} file(s) changed
          </div>
          <pre className="block" style={{ whiteSpace: "pre-wrap" }}>{showLog.log || "(no log)"}</pre>
        </Modal>
      ) : null}

      {creating && data ? (
        <CreateScheduler
          kinds={data.kinds}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            reload();
          }}
        />
      ) : null}
    </>
  );
}

function CreateScheduler({
  kinds,
  onClose,
  onCreated,
}: {
  kinds: SchedulerKind[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const accounts = useQuery<{ accounts: Account[] }>("/api/accounts");
  const [form, setForm] = useState({
    kind: kinds[0]?.name ?? "",
    projectId: "",
    accountId: "",
    title: "",
    schedule: "0 */6 * * *",
    targetPath: "",
    url: "",
  });
  const [error, setError] = useState<Error | null>(null);
  const kind = kinds.find((k) => k.name === form.kind);
  const usable = (accounts.data?.accounts ?? []).filter(
    (a) => !kind?.accountKinds || kind.accountKinds.includes(a.kind),
  );

  return (
    <Modal
      title="New scheduler"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={!form.projectId || !form.kind}
            onClick={async () => {
              try {
                await api("/api/schedulers", {
                  body: {
                    kind: form.kind,
                    projectId: form.projectId,
                    accountId: form.accountId,
                    title: form.title,
                    schedule: form.schedule,
                    targetPath: form.targetPath,
                    options: form.url ? { url: form.url } : {},
                  },
                });
                onCreated();
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
      <Field label="What it does">
        <select value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
          {kinds.map((k) => (
            <option key={k.name} value={k.name}>
              {k.title}
            </option>
          ))}
        </select>
      </Field>
      {kind?.description ? <p className="hint">{kind.description}</p> : null}

      <Field label="Into which project">
        <select value={form.projectId} onChange={(e) => setForm({ ...form, projectId: e.target.value })}>
          <option value="">— pick one —</option>
          {(projects.data?.projects ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.groupSlug ? `${p.groupSlug} / ` : ""}
              {p.title}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Name">
        <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} />
      </Field>

      {kind?.accountKinds?.length ? (
        <Field
          label={kind.accountRequired ? "Account (required)" : "Account (only if the source asks for a login)"}
        >
          <select value={form.accountId} onChange={(e) => setForm({ ...form, accountId: e.target.value })}>
            <option value="">— none —</option>
            {usable.map((a) => (
              <option key={a.id} value={a.id}>
                {a.title}
                {a.needsSecret ? " (needs its password again)" : ""}
              </option>
            ))}
          </select>
        </Field>
      ) : null}

      <Field label="URL" hint="For the kinds that fetch something: an ICS address, a feed, any URL.">
        <input value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} />
      </Field>

      <div className="row">
        <Field label="Schedule" hint="A cron expression, or manual.">
          <input value={form.schedule} onChange={(e) => setForm({ ...form, schedule: e.target.value })} />
        </Field>
        <Field label="Target folder in the project">
          <input value={form.targetPath} onChange={(e) => setForm({ ...form, targetPath: e.target.value })} />
        </Field>
      </div>
      <p className="hint">
        What a run pulls lands as files. Delete one and it comes back on the next run — that is intended.
      </p>
    </Modal>
  );
}
