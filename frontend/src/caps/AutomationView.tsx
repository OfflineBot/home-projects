import { useState } from "react";
import { Icon } from "../components/Icon";
import { Fields } from "../components/Fields";
import { Copyable, Empty, ErrorBox, Field, Fold, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useMeta, useQuery } from "../lib/store";

interface Rule {
  name: string;
  description?: string;
  enabled?: boolean;
  trigger: { type: string; cron?: string; event?: string; project?: string; path?: string; secret?: string };
  actions: ({ run: string } & Record<string, any>)[];
}

/** The parameters that are a choice rather than a line to type into. */


/**
 * The lamps this project can reach, written down once.
 *
 * Without this every card and every rule carries an IP address around, and the
 * day the network is renumbered means editing all of them. A lamp has a name
 * here; cards and rules use the name. The switches are on the page too, because
 * the first thing anybody wants after writing down a lamp is to see it come on.
 */
interface Lamp {
  name: string;
  host?: string;
  /** A name may hold a roomful: switching it switches all of them at once. */
  hosts?: string[];
}

/** Everything one name reaches, however it was written down. */
function addressesOf(lamp: Lamp): string[] {
  return [lamp.host ?? "", ...(lamp.hosts ?? [])]
    .flatMap((raw) => raw.split(/[\s,]+/))
    .map((h) => h.trim())
    .filter(Boolean);
}

function Lights({ project, onFailed }: { project: Project; onFailed: (e: Error) => void }) {
  const { data, reload } = useQuery<{ lights: Lamp[] }>(`/api/projects/${project.id}/automation/lights`);
  const [draft, setDraft] = useState<Lamp[] | null>(null);
  const [busy, setBusy] = useState("");
  const [said, setSaid] = useState("");
  const lights = draft ?? data?.lights ?? [];

  const save = async (next: Lamp[]) => {
    try {
      await api(`/api/projects/${project.id}/automation/lights`, { method: "PUT", body: { lights: next } });
      setDraft(null);
      reload();
    } catch (err) {
      onFailed(err as Error);
    }
  };

  const send = async (light: { name: string }, body: Record<string, unknown>) => {
    setBusy(light.name);
    setSaid("");
    try {
      const answer = await api<{ light: { on: boolean; reachable: boolean }; note?: string }>(
        `/api/projects/${project.id}/automation/light`,
        { body: { host: light.name, ...body } },
      );
      setSaid(answer.light?.reachable ? `${light.name}: ${answer.light.on ? "on" : "off"}` : `${light.name}: not answering`);
    } catch (err) {
      setSaid((err as Error).message);
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="lights-block">
      <div className="lights-head">
        <strong className="grow">Lights</strong>
        {said ? <span className="meta">{said}</span> : null}
        {!project.readOnly ? (
          <button className="btn small" onClick={() => setDraft([...lights, { name: "", hosts: [] }])}>
            <Icon name="plus" size={13} /> A light
          </button>
        ) : null}
      </div>

      {lights.length === 0 && draft === null ? (
        <p className="meta">
          None yet. A light is a name and one or more WLED addresses — a whole room under one name if you like.
          After that the cards and the rules use the name.
        </p>
      ) : null}

      {lights.map((light, i) => (
        <div key={i} className="lights-row">
          {draft ? (
            <>
              <input
                value={light.name}
                placeholder="Desk"
                onChange={(e) =>
                  setDraft(draft.map((l, j) => (j === i ? { ...l, name: e.target.value } : l)))
                }
              />
              <input
                className="mono"
                value={addressesOf(light).join(", ")}
                placeholder="192.168.178.60, 192.168.178.49 — as many as belong together"
                onChange={(e) =>
                  setDraft(
                    draft.map((l, j) =>
                      j === i
                        ? { name: l.name, hosts: e.target.value.split(/[\s,]+/).map((h) => h.trim()).filter(Boolean) }
                        : l,
                    ),
                  )
                }
              />
              <button
                className="btn ghost icon"
                aria-label={`Remove ${light.name}`}
                onClick={() => setDraft(draft.filter((_, j) => j !== i))}
              >
                <Icon name="trash" size={13} />
              </button>
            </>
          ) : (
            <>
              <Icon name="lightbulb" size={15} />
              <strong>{light.name}</strong>
              <span className="meta mono grow">
                {addressesOf(light).length > 1
                  ? `${addressesOf(light).length} lamps · ${addressesOf(light).join(", ")}`
                  : addressesOf(light)[0]}
              </span>
              <button className="btn small" disabled={busy === light.name} onClick={() => void send(light, { power: "on" })}>
                on
              </button>
              <button className="btn small" disabled={busy === light.name} onClick={() => void send(light, { power: "off" })}>
                off
              </button>
              <button
                className="btn small"
                disabled={busy === light.name}
                onClick={() => void send(light, { power: "toggle" })}
              >
                toggle
              </button>
              <input
                type="color"
                className="light-colour"
                aria-label={`Colour of ${light.name}`}
                defaultValue="#b4befe"
                onChange={(e) => void send(light, { color: e.target.value })}
              />
            </>
          )}
        </div>
      ))}

      {draft ? (
        <div className="lights-row">
          <button
            className="btn small primary"
            onClick={() => void save(draft.filter((l) => l.name && addressesOf(l).length > 0))}
          >
            Save
          </button>
          <button className="btn small" onClick={() => setDraft(null)}>
            Cancel
          </button>
        </div>
      ) : null}
    </div>
  );
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
  const [said, setSaid] = useState<{ rule: string; text: string; log: string; bad?: boolean } | null>(null);
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
      // Not the log. Pressing a button is not a debugging session: it says
      // what happened in one line, and the log is a click away for the day
      // something is wrong.
      const status = result.run?.status === "error" ? "did not work" : "done";
      setSaid({
        rule: rule.name,
        text: result.error || result.run?.message || status,
        log: result.run?.log ?? "",
        bad: Boolean(result.error) || result.run?.status === "error",
      });
    } catch (err) {
      // A rule that fails is an ordinary outcome, not a fault of the page. The
      // run came back with the failure, so the log is there too.
      const detail = (err as { detail?: { run?: { log?: string } } }).detail;
      setSaid({ rule: rule.name, text: (err as Error).message, log: detail?.run?.log ?? "", bad: true });
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

      {said ? (
        <div className={said.bad ? "ran bad" : "ran"}>
          <Icon name={said.bad ? "alert" : "check"} size={14} />
          <strong>{said.rule}</strong>
          <span className="grow">{said.text}</span>
          {said.log ? (
            <button className="btn ghost small" onClick={() => setShowLog(said.log)}>
              what it did
            </button>
          ) : null}
          <button className="btn ghost icon" aria-label="Hide" onClick={() => setSaid(null)}>
            <Icon name="x" size={13} />
          </button>
        </div>
      ) : null}

      <Lights project={project} onFailed={setActionError} />

      <div style={{ display: "flex", gap: 8, marginBottom: 14 }}>
        <strong style={{ flex: 1 }}>
          {data?.rules.length ?? 0} {data?.rules.length === 1 ? "rule" : "rules"}
        </strong>
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
          <Fold title="The log" hint={`${showLog.split("\n").filter(Boolean).length} lines`} open>
            <pre className="block" style={{ whiteSpace: "pre-wrap" }}>{showLog || "(empty)"}</pre>
          </Fold>
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
            {/* The server says what each parameter is — a colour is a colour,
                an effect is one of a list, a machine is one of the accounts.
                Drawing it is the one renderer's job, not this file's. */}
            <Fields
              specs={
                spec?.fields?.length
                  ? spec.fields
                  : (spec?.params ?? []).map((param) => ({ name: param, label: param }))
              }
              values={action}
              onChange={(next) =>
                setDraft({
                  ...draft,
                  actions: draft.actions.map((a, j) => (i === j ? { ...(next as typeof a), run: a.run } : a)),
                })
              }
            />
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
