import { useEffect, useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Fold, Modal, Section, Spinner, formatDate } from "../components/ui";
import NewAccount from "../components/NewAccount";
import {
  api,
  type Account,
  type AccountKind,
  type Filter,
  type Project,
  type Scheduler,
  type SchedulerKind,
  type SchedulerRun,
} from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/** What pulls data in from outside, and what came of every run. */
export default function Schedulers() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<{ schedulers: Scheduler[]; kinds: SchedulerKind[] }>(
    "/api/schedulers",
  );
  const runs = useQuery<{ runs: SchedulerRun[] }>("/api/runs");
  const anyRunning = (data?.schedulers ?? []).some((s) => s.running);

  // While something is running, the page keeps itself honest: the button stays
  // dark and the result appears without a reload.
  useEffect(() => {
    if (!anyRunning) return;
    const timer = setInterval(() => {
      reload();
      runs.reload();
    }, 3000);
    return () => clearInterval(timer);
  }, [anyRunning, reload]);

  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Scheduler | null>(null);
  const [showLog, setShowLog] = useState<SchedulerRun | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);

  if (!session.user) return <Empty icon="lock">Sign in to see the schedulers.</Empty>;

  const run = async (s: Scheduler, fresh = false) => {
    setBusy(s.id);
    setActionError(null);
    try {
      const result = await api<{ run: SchedulerRun }>(
        `/api/schedulers/${s.id}/run${fresh ? "?fresh=true" : ""}`,
        { method: "POST" },
      );
      setShowLog(result.run);
    } catch (err) {
      setActionError(err as Error);
    } finally {
      setBusy(null);
      reload();
      runs.reload();
    }
  };

  /**
   * A rebuild does not empty the folder first: it fetches everything again and
   * then removes what the source no longer has. Same end state, without a
   * window where the material is gone because the fetch failed halfway.
   */
  const rebuild = async (s: Scheduler) => {
    const ok = confirm(
      `Rebuild "${s.title || s.kind}"?\n\n` +
        "Every file is fetched again, and anything the source no longer has is removed from the folders " +
        "this scheduler writes into. Whatever you keep beside those folders stays.",
    );
    if (ok) await run(s, true);
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Schedulers</h1>
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
              {s.filterNames?.length ? ` · ${s.filterNames.join(" → ")}` : ""}
            </div>
            {s.pausedReason ? <div className="warning" style={{ margin: 0 }}>{s.pausedReason}</div> : null}
            <div className="tile-foot">
              <span className={`badge ${s.lastStatus === "ok" ? "good" : s.lastStatus === "error" ? "bad" : ""}`}>
                {s.lastStatus || "never run"}
              </span>
              {!s.enabled ? <span className="badge warn">paused</span> : null}
              {s.running ? (
                <span className="badge good">
                  <Icon name="refresh" size={12} /> running
                </span>
              ) : null}
              <div style={{ flex: 1 }} />
              <button
                className="btn small primary"
                disabled={busy === s.id || s.running}
                title={s.running ? "It is running — a second start would write over the first" : "Run it now"}
                onClick={() => run(s)}
              >
                <Icon name="play" size={13} /> {s.running || busy === s.id ? "Running…" : "Run now"}
              </button>
              <button
                className="btn small"
                disabled={busy === s.id || s.running}
                title="Fetch everything again and remove what the source no longer has"
                onClick={() => rebuild(s)}
              >
                <Icon name="refresh" size={13} /> Rebuild
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
                disabled={s.running}
                title={s.running ? "It is running — wait for it to finish" : "Delete this scheduler"}
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
          <Fold
            title="The log"
            hint={`${(showLog.log ?? "").split("\n").filter(Boolean).length} lines`}
            open={showLog.status !== "ok"}
          >
            <pre className="block" style={{ whiteSpace: "pre-wrap" }}>{showLog.log || "(no log)"}</pre>
          </Fold>
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
  const accounts = useQuery<{ accounts: Account[]; kinds: AccountKind[] }>("/api/accounts");
  const [makingAccount, setMakingAccount] = useState(false);
  const filters = useQuery<{ filters: Filter[] }>("/api/filters");
  const [form, setForm] = useState({
    kind: existing?.kind ?? kinds[0]?.name ?? "",
    projectId: existing?.projectId ?? "",
    accountId: existing?.accountId ?? "",
    title: existing?.title ?? "",
    schedule: existing?.schedule ?? "0 */6 * * *",
    targetPath: existing?.targetPath ?? "",
    filterIds: existing?.filterIds ?? [],
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
            projectId: form.projectId,
            schedule: form.schedule,
            targetPath: form.targetPath,
            accountId: form.accountId,
            filterIds: form.filterIds,
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
            filterIds: form.filterIds,
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
      wide
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
        <>
          <p className="hint" style={{ marginTop: 0 }}>
            <strong>{kind?.title ?? existing.kind}</strong> — what it does cannot be changed; everything
            else can.
          </p>
          <Field
            label="Project"
            hint={
              form.projectId !== existing.projectId
                ? "From the next run on it writes there. What it already wrote stays where it is."
                : undefined
            }
          >
            <select value={form.projectId} onChange={(e) => setForm({ ...form, projectId: e.target.value })}>
              {(projects.data?.projects ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.groupSlug ? `${p.groupSlug} / ` : ""}
                  {p.title}
                </option>
              ))}
            </select>
          </Field>
        </>
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

          <Field label="Project" hint="It writes into this project's files.">
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

      <Section title="What and where" />

      <Field label="Name">
        <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} />
      </Field>

      {makingAccount ? (
        <NewAccount
          kinds={accounts.data?.kinds ?? []}
          only={kind?.accountKinds ?? undefined}
          onClose={() => setMakingAccount(false)}
          onCreated={(id) => {
            setMakingAccount(false);
            setForm({ ...form, accountId: id });
            accounts.reload();
          }}
        />
      ) : null}

      {kind?.accountKinds?.length ? (
        <Field
          label={kind.accountRequired ? "Account (required)" : "Account (only if the source asks for a login)"}
        >
          <select
            value={form.accountId}
            onChange={(e) => {
              if (e.target.value === "__new") {
                setMakingAccount(true);
                return;
              }
              setForm({ ...form, accountId: e.target.value });
            }}
          >
            <option value="">— none —</option>
            {usable.map((a) => (
              <option key={a.id} value={a.id}>
                {a.title}
                {a.needsSecret ? " (needs its password again)" : ""}
              </option>
            ))}
            <option value="__new">＋ a new account…</option>
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

      <Section title="Sorting" />

      <Field
        label="Filters"
        hint="Sorts what this fetches straight away, so no project is needed in between. Several run in the order below, and the first rule that matches takes the file."
      >
        {form.filterIds.length ? (
          <div className="list" style={{ marginBottom: 8 }}>
            {form.filterIds.map((id, i) => {
              const f = (filters.data?.filters ?? []).find((x) => x.id === id);
              return (
                <div key={id} className="list-row">
                  <span className="meta" style={{ width: 18 }}>{i + 1}.</span>
                  <span className="grow">{f?.title ?? "a filter that is gone"}</span>
                  <button
                    className="btn ghost icon"
                    aria-label="Earlier"
                    disabled={i === 0}
                    onClick={() => {
                      const next = [...form.filterIds];
                      [next[i - 1], next[i]] = [next[i], next[i - 1]];
                      setForm({ ...form, filterIds: next });
                    }}
                  >
                    <Icon name="chevronUp" size={14} />
                  </button>
                  <button
                    className="btn ghost icon"
                    aria-label="Later"
                    disabled={i === form.filterIds.length - 1}
                    onClick={() => {
                      const next = [...form.filterIds];
                      [next[i + 1], next[i]] = [next[i], next[i + 1]];
                      setForm({ ...form, filterIds: next });
                    }}
                  >
                    <Icon name="chevronDown" size={14} />
                  </button>
                  <button
                    className="btn ghost icon"
                    aria-label={`Remove ${f?.title ?? "it"}`}
                    onClick={() =>
                      setForm({ ...form, filterIds: form.filterIds.filter((x) => x !== id) })
                    }
                  >
                    <Icon name="x" size={14} />
                  </button>
                </div>
              );
            })}
          </div>
        ) : null}
        <select
          value=""
          onChange={(e) => {
            if (!e.target.value) return;
            setForm({ ...form, filterIds: [...form.filterIds, e.target.value] });
          }}
        >
          <option value="">— add a filter —</option>
          {(filters.data?.filters ?? [])
            .filter((f) => !form.filterIds.includes(f.id))
            .map((f) => (
              <option key={f.id} value={f.id}>
                {f.title}
              </option>
            ))}
        </select>
      </Field>

      <Section title="When" />

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
        <Field label="Folder" hint="Inside the project. Empty means the project itself.">
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


    </Modal>
  );
}
