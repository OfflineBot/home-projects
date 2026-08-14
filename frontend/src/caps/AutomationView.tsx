import { useState } from "react";
import { Icon } from "../components/Icon";
import { Copyable, Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useMeta, useQuery } from "../lib/store";

interface Rule {
  name: string;
  description?: string;
  enabled?: boolean;
  trigger: { type: string; cron?: string; event?: string; project?: string; path?: string; secret?: string };
  actions: ({ run: string } & Record<string, any>)[];
}

/**
 * Rules made of trigger plus actions, stored as automation.yaml in the
 * project — so they are versioned and exportable. This is what replaced the
 * old dedicated "Lights" and "PC" pages.
 */
export default function AutomationView({ project }: { project: Project; reload: () => void }) {
  const meta = useMeta();
  const { data, error, loading, reload } = useQuery<{ rules: Rule[]; error?: string; warning?: string }>(
    `/api/projects/${project.id}/automation/rules`,
  );
  const runs = useQuery<{ runs: { id: number; rule: string; trigger: string; status: string; startedAt: string; log: string }[] }>(
    `/api/projects/${project.id}/automation/runs`,
  );
  const [editing, setEditing] = useState<Rule | null>(null);
  const [showLog, setShowLog] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);

  const save = async (rules: Rule[]) => {
    setActionError(null);
    try {
      await api(`/api/projects/${project.id}/automation/rules`, { method: "PUT", body: { rules } });
      reload();
    } catch (err) {
      setActionError(err as Error);
    }
  };

  const run = async (rule: Rule) => {
    setBusy(rule.name);
    setActionError(null);
    try {
      const result = await api<{ run: any; error?: string }>(
        `/api/projects/${project.id}/automation/rules/${encodeURIComponent(rule.name)}/run`,
        { method: "POST" },
      );
      if (result.error) setActionError(new Error(result.error));
      setShowLog(result.run?.log ?? null);
    } catch (err) {
      setActionError(err as Error);
    } finally {
      setBusy(null);
      runs.reload();
    }
  };

  return (
    <div>
      <ErrorBox error={actionError ?? error} onRetry={reload} />
      {data?.error ? <div className="warning">{data.error}</div> : null}
      {data?.warning ? <div className="warning">{data.warning}</div> : null}
      {loading && !data ? <Spinner /> : null}

      <div style={{ display: "flex", gap: 8, marginBottom: 14 }}>
        <strong style={{ flex: 1 }}>{data?.rules.length ?? 0} rules</strong>
        {!project.readOnly ? (
          <button
            className="btn"
            onClick={() => setEditing({ name: "", trigger: { type: "button" }, actions: [{ run: "http" }] })}
          >
            <Icon name="plus" size={15} /> New rule
          </button>
        ) : null}
      </div>

      {data && data.rules.length === 0 ? (
        <Empty icon="zap">No rules yet.</Empty>
      ) : null}

      <div className="tiles">
        {data?.rules.map((rule) => (
          <div key={rule.name} className="tile">
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name="zap" />
              </span>
              <div style={{ minWidth: 0 }}>
                <h3>{rule.name}</h3>
                <div className="sub">
                  {rule.trigger.type}
                  {rule.trigger.cron ? ` · ${rule.trigger.cron}` : ""}
                  {rule.trigger.event ? ` · ${rule.trigger.event}` : ""}
                </div>
              </div>
            </div>
            <div className="sub">
              {rule.actions.map((a, i) => (
                <span key={i} className="badge" style={{ marginRight: 6 }}>
                  {a.run}
                </span>
              ))}
            </div>
            {rule.trigger.type === "webhook" && rule.trigger.secret ? (
              <Copyable
                value={`${meta?.publicUrl ?? ""}/api/capabilities/automation/hooks/${project.slug}/${encodeURIComponent(rule.name)}?secret=${rule.trigger.secret}`}
              />
            ) : null}
            <div className="tile-foot">
              <button className="btn small primary" disabled={busy === rule.name} onClick={() => run(rule)}>
                <Icon name="play" size={13} /> Run now
              </button>
              {!project.readOnly ? (
                <>
                  <button className="btn small" onClick={() => setEditing(rule)}>
                    Edit
                  </button>
                  <button
                    className="btn small danger"
                    onClick={() => save((data?.rules ?? []).filter((r) => r.name !== rule.name))}
                  >
                    Delete
                  </button>
                </>
              ) : null}
            </div>
          </div>
        ))}
      </div>

      <h3 style={{ marginTop: 26 }}>Runs</h3>
      {runs.data?.runs.length ? (
        <div className="list">
          {runs.data.runs.map((r) => (
            <button
              key={r.id}
              className="list-row"
              style={{ border: "none", background: "none", textAlign: "left", cursor: "pointer", width: "100%" }}
              onClick={() => setShowLog(r.log)}
            >
              <span className={`badge ${r.status === "ok" ? "good" : "bad"}`}>{r.status}</span>
              <span className="grow">{r.rule}</span>
              <span className="meta">{r.trigger}</span>
              <span className="meta">{formatDate(r.startedAt)}</span>
            </button>
          ))}
        </div>
      ) : (
        <p style={{ color: "var(--ctp-subtext0)" }}>Nothing has run yet. Failed runs show up here too.</p>
      )}

      {showLog !== null ? (
        <Modal title="Run log" onClose={() => setShowLog(null)}>
          <pre className="block" style={{ whiteSpace: "pre-wrap" }}>{showLog || "(empty)"}</pre>
        </Modal>
      ) : null}

      {editing ? (
        <RuleDialog
          rule={editing}
          existing={data?.rules ?? []}
          onClose={() => setEditing(null)}
          onSave={async (rules) => {
            await save(rules);
            setEditing(null);
          }}
        />
      ) : null}
    </div>
  );
}

function RuleDialog({
  rule,
  existing,
  onClose,
  onSave,
}: {
  rule: Rule;
  existing: Rule[];
  onClose: () => void;
  onSave: (rules: Rule[]) => Promise<void>;
}) {
  const meta = useMeta();
  const [draft, setDraft] = useState<Rule>(JSON.parse(JSON.stringify(rule)));
  const [error, setError] = useState<Error | null>(null);

  const actions = meta?.actions ?? [];

  return (
    <Modal
      title={rule.name ? `Rule · ${rule.name}` : "New rule"}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!draft.name.trim()}
            onClick={async () => {
              try {
                const rest = existing.filter((r) => r.name !== rule.name);
                await onSave([...rest, draft]);
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
      <Field label="Name">
        <input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} autoFocus />
      </Field>

      <Field label="Trigger">
        <select
          value={draft.trigger.type}
          onChange={(e) => setDraft({ ...draft, trigger: { ...draft.trigger, type: e.target.value } })}
        >
          <option value="button">Button — run it by hand or from the dashboard</option>
          <option value="schedule">Schedule — cron</option>
          <option value="event">Event — a file changes, a run finishes</option>
          <option value="webhook">Webhook — its own URL with a secret</option>
        </select>
      </Field>

      {draft.trigger.type === "schedule" ? (
        <Field label="Cron" hint="Five fields, e.g. 0 */6 * * *">
          <input
            value={draft.trigger.cron ?? ""}
            onChange={(e) => setDraft({ ...draft, trigger: { ...draft.trigger, cron: e.target.value } })}
            placeholder="*/5 * * * *"
          />
        </Field>
      ) : null}

      {draft.trigger.type === "event" ? (
        <div className="row">
          <Field label="Which event">
            <select
              value={draft.trigger.event ?? "file.changed"}
              onChange={(e) => setDraft({ ...draft, trigger: { ...draft.trigger, event: e.target.value } })}
            >
              <option value="file.changed">a file changes</option>
              <option value="scheduler.finished">a scheduler run finishes</option>
              <option value="git.pushed">something was pushed</option>
            </select>
          </Field>
          <Field label="Only for this project (slug)">
            <input
              value={draft.trigger.project ?? ""}
              onChange={(e) => setDraft({ ...draft, trigger: { ...draft.trigger, project: e.target.value } })}
            />
          </Field>
        </div>
      ) : null}

      {draft.trigger.type === "webhook" ? (
        <Field label="Secret" hint="Whoever calls the URL has to send this.">
          <input
            value={draft.trigger.secret ?? ""}
            onChange={(e) => setDraft({ ...draft, trigger: { ...draft.trigger, secret: e.target.value } })}
          />
        </Field>
      ) : null}

      <h3 style={{ fontSize: 15 }}>Actions, in order</h3>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        If one fails the chain stops and the reason is in the log.
      </p>

      {draft.actions.map((action, i) => {
        const spec = actions.find((a) => a.name === action.run);
        return (
          <div key={i} className="tile" style={{ marginBottom: 12 }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <select
                value={action.run}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    actions: draft.actions.map((a, j) => (i === j ? { run: e.target.value } : a)),
                  })
                }
              >
                {actions.map((a) => (
                  <option key={a.name} value={a.name}>
                    {a.title}
                  </option>
                ))}
              </select>
              <button
                className="btn ghost icon"
                onClick={() => setDraft({ ...draft, actions: draft.actions.filter((_, j) => j !== i) })}
              >
                <Icon name="trash" size={15} />
              </button>
            </div>
            <div className="sub">{spec?.description}</div>
            {(spec?.params ?? []).map((param) => (
              <Field key={param} label={param}>
                <input
                  value={typeof action[param] === "object" ? JSON.stringify(action[param]) : (action[param] ?? "")}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      actions: draft.actions.map((a, j) => (i === j ? { ...a, [param]: e.target.value } : a)),
                    })
                  }
                />
              </Field>
            ))}
          </div>
        );
      })}

      <button
        className="btn"
        onClick={() => setDraft({ ...draft, actions: [...draft.actions, { run: actions[0]?.name ?? "http" }] })}
      >
        <Icon name="plus" size={15} /> Add an action
      </button>
    </Modal>
  );
}
