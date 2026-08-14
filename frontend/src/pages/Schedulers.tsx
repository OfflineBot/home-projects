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
  const [editing, setEditing] = useState<Scheduler | null>(null);
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
              <button className="btn small" onClick={() => setEditing(s)}>
                <Icon name="settings" size={13} /> Edit
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
        <SchedulerDialog
          kinds={data.kinds}
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            reload();
          }}
        />
      ) : null}

      {editing && data ? (
        <SchedulerDialog
          kinds={data.kinds}
          existing={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
            runs.reload();
          }}
        />
      ) : null}
    </>
  );
}

/**
 * Cron is exact and unreadable. The common answers are offered by name, the
 * field stays open for the ones that are not, and what is stored is a cron
 * expression either way — the server never learns about this list.
 */
const SCHEDULES: { value: string; label: string }[] = [
  { value: "manual", label: "by hand only" },
  { value: "*/15 * * * *", label: "every 15 minutes" },
  { value: "0 * * * *", label: "hourly" },
  { value: "0 */6 * * *", label: "every six hours" },
  { value: "0 5 * * *", label: "nightly, at 05:00" },
  { value: "0 5 * * 1", label: "Mondays, at 05:00" },
];

/**
 * One dialog for both: making a scheduler and changing one.
 *
 * What can be changed afterwards is everything that is a decision — the name,
 * the schedule, the target folder, the account, what the kind can be told.
 * What it *is* and where it writes are not, because that would silently turn
 * one scheduler into a different one; for that, make a new one and delete this.
 */
function SchedulerDialog({
  kinds,
  existing,
  onClose,
  onSaved,
}: {
  kinds: SchedulerKind[];
  existing?: Scheduler;
  onClose: () => void;
  onSaved: () => void;
}) {
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const accounts = useQuery<{ accounts: Account[] }>("/api/accounts");
  const [form, setForm] = useState({
    kind: existing?.kind ?? kinds[0]?.name ?? "",
    projectId: existing?.projectId ?? "",
    accountId: existing?.accountId ?? "",
    title: existing?.title ?? "",
    schedule: existing?.schedule ?? "0 */6 * * *",
    targetPath: existing?.targetPath ?? "",
    enabled: existing?.enabled ?? true,
  });
  // Each kind says what it can be told; this holds those answers.
  const [options, setOptions] = useState<Record<string, string | number | boolean>>(
    (existing?.options ?? {}) as Record<string, string | number | boolean>,
  );
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const kind = kinds.find((k) => k.name === form.kind);
  const usable = (accounts.data?.accounts ?? []).filter(
    (a) => !kind?.accountKinds || kind.accountKinds.includes(a.kind),
  );
  const known = SCHEDULES.some((s) => s.value === form.schedule);
  const [custom, setCustom] = useState(!known);
  const project = (projects.data?.projects ?? []).find((p) => p.id === form.projectId);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      if (existing) {
        await api(`/api/schedulers/${existing.id}`, {
          method: "PATCH",
          body: {
            title: form.title,
            schedule: form.schedule,
            targetPath: form.targetPath,
            accountId: form.accountId,
            enabled: form.enabled,
            options,
          },
        });
      } else {
        await api("/api/schedulers", {
          body: {
            kind: form.kind,
            projectId: form.projectId,
            accountId: form.accountId,
            title: form.title,
            schedule: form.schedule,
            targetPath: form.targetPath,
            options,
          },
        });
      }
      onSaved();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={existing ? existing.title || existing.kind : "New scheduler"}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy || !form.projectId || !form.kind}
            onClick={save}
          >
            {existing ? "Save" : "Create"}
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      {existing ? (
        <p className="hint" style={{ marginTop: 0 }}>
          <strong>{kind?.title ?? existing.kind}</strong> → {existing.projectSlug}
          {existing.targetPath ? `/${existing.targetPath}` : ""}. What it does and which project it writes
          into cannot be changed here — everything else can.
        </p>
      ) : (
        <>
          <Field label="What it does">
            <select
              value={form.kind}
              onChange={(e) => {
                setForm({ ...form, kind: e.target.value });
                setOptions({});
              }}
            >
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
          {project?.readOnly ? (
            <div className="warning">{project.title} is read-only — a run would be refused.</div>
          ) : null}
        </>
      )}

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

      {(kind?.options ?? []).map((o) =>
        o.type === "bool" ? (
          <div key={o.name}>
            <label className="check">
              <input
                type="checkbox"
                checked={options[o.name] === undefined ? Boolean(o.default) : Boolean(options[o.name])}
                onChange={(e) => setOptions({ ...options, [o.name]: e.target.checked })}
              />
              <span>{o.label}</span>
            </label>
            {o.hint ? <p className="hint" style={{ marginTop: -8 }}>{o.hint}</p> : null}
          </div>
        ) : o.type === "textarea" ? (
          <Field key={o.name} label={o.label} hint={o.hint}>
            <textarea
              style={{ minHeight: 92, fontFamily: "var(--mono)", fontSize: 13 }}
              placeholder={o.placeholder}
              value={String(options[o.name] ?? "")}
              onChange={(e) => setOptions({ ...options, [o.name]: e.target.value })}
            />
          </Field>
        ) : (
          <Field key={o.name} label={o.label} hint={o.hint}>
            <input
              type={o.type === "number" ? "number" : "text"}
              placeholder={o.placeholder}
              value={String(options[o.name] ?? "")}
              onChange={(e) =>
                setOptions({
                  ...options,
                  [o.name]: o.type === "number" ? Number(e.target.value) : e.target.value,
                })
              }
            />
          </Field>
        ),
      )}

      <div className="row">
        <Field label="How often">
          <select
            value={custom ? "custom" : form.schedule}
            onChange={(e) => {
              if (e.target.value === "custom") {
                setCustom(true);
                return;
              }
              setCustom(false);
              setForm({ ...form, schedule: e.target.value });
            }}
          >
            {SCHEDULES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
            <option value="custom">a cron expression of my own…</option>
          </select>
        </Field>
        <Field label="Target folder in the project" hint="Empty means the project itself.">
          <input
            value={form.targetPath}
            onChange={(e) => setForm({ ...form, targetPath: e.target.value })}
            placeholder="e.g. moodle"
          />
        </Field>
      </div>

      {custom ? (
        <Field label="Cron expression" hint="minute hour day month weekday — or the word manual.">
          <input
            value={form.schedule}
            onChange={(e) => setForm({ ...form, schedule: e.target.value })}
            placeholder="0 */6 * * *"
          />
        </Field>
      ) : null}

      {existing ? (
        <>
          <label className="check">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            <span>Running — unticked it stays put until you say otherwise</span>
          </label>
          {existing.pausedReason ? (
            <div className="warning">
              Paused: {existing.pausedReason}. Saving with the tick set clears that.
            </div>
          ) : null}
        </>
      ) : null}

      <p className="hint">
        What a run pulls lands as files. Delete one and it comes back on the next run — that is intended.
      </p>
    </Modal>
  );
}
