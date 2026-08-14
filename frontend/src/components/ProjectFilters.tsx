import { useState } from "react";
import { api, type Filter, type Project } from "../lib/api";
import { useQuery } from "../lib/store";
import { Icon } from "./Icon";
import { ErrorBox, Field } from "./ui";

interface Attached extends Filter {
  automatic: boolean;
}

interface Step {
  from: string;
  to: string;
  project?: string;
  rule?: string;
  note?: string;
  isDir?: boolean;
}

/**
 * The filters this project uses.
 *
 * A filter is written once, in its own menu, and a project picks up the ones it
 * wants — not the other way round. A scheduler only knows how to fetch; where
 * the things it fetched belong is the project's business, and a project nothing
 * fetches into can use the same rules by hand.
 */
export default function ProjectFilters({ project }: { project: Project }) {
  const mine = useQuery<{ filters: Attached[] }>(`/api/projects/${project.id}/filters`);
  const all = useQuery<{ filters: Filter[] }>("/api/filters");
  const [error, setError] = useState<Error | null>(null);
  const [plan, setPlan] = useState<Step[] | null>(null);
  const [busy, setBusy] = useState(false);

  const attached = mine.data?.filters ?? [];
  const available = (all.data?.filters ?? []).filter((f) => !attached.some((a) => a.id === f.id));

  const run = async (apply: boolean) => {
    setBusy(true);
    setError(null);
    try {
      const res = await api<{ steps: Step[] }>(`/api/projects/${project.id}/filter`, {
        body: { apply },
      });
      setPlan(res.steps);
      if (apply) mine.reload();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <ErrorBox error={error} />

      {attached.length ? (
        <div className="list" style={{ marginBottom: 12 }}>
          {attached.map((f) => (
            <div key={f.id} className="list-row">
              <Icon name="search" size={15} />
              <span className="grow">
                {f.title}
                <span className="meta"> · {f.rules.length} rules</span>
              </span>
              <label className="check" style={{ margin: 0 }}>
                <input
                  type="checkbox"
                  checked={f.automatic}
                  onChange={async (e) => {
                    await api(`/api/projects/${project.id}/filters`, {
                      body: { filter: f.id, automatic: e.target.checked },
                    });
                    mine.reload();
                  }}
                />
                <span className="meta">by itself</span>
              </label>
              <button
                className="btn ghost icon"
                aria-label={`Remove ${f.title}`}
                onClick={async () => {
                  await api(`/api/projects/${project.id}/filters/${f.id}`, { method: "DELETE" });
                  mine.reload();
                }}
              >
                <Icon name="x" size={15} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <p className="meta">None yet.</p>
      )}

      {available.length ? (
        <Field label="Add one" hint="Written under Filters, used here.">
          <select
            value=""
            onChange={async (e) => {
              if (!e.target.value) return;
              await api(`/api/projects/${project.id}/filters`, { body: { filter: e.target.value } });
              mine.reload();
            }}
          >
            <option value="">— pick one —</option>
            {available.map((f) => (
              <option key={f.id} value={f.id}>
                {f.title}
              </option>
            ))}
          </select>
        </Field>
      ) : null}

      {attached.length ? (
        <div style={{ display: "flex", gap: 8 }}>
          <button className="btn small" disabled={busy} onClick={() => run(false)}>
            What would it do?
          </button>
          <button className="btn small primary" disabled={busy || !plan?.length} onClick={() => run(true)}>
            Sort now
          </button>
        </div>
      ) : null}

      {plan ? (
        plan.length === 0 ? (
          <p className="meta" style={{ marginTop: 10 }}>
            Nothing here matches a rule.
          </p>
        ) : (
          <div className="list" style={{ marginTop: 10 }}>
            {plan.map((s, i) => (
              <div key={i} className="list-row">
                <Icon name={s.isDir ? "folder" : "file"} size={14} />
                <span className="grow mono">{s.from}</span>
                <Icon name="chevronRight" size={13} />
                <span className="mono">{[s.project, s.to].filter(Boolean).join(" / ")}</span>
                {s.note ? <span className="badge bad">{s.note}</span> : null}
              </div>
            ))}
          </div>
        )
      ) : null}
    </div>
  );
}
