import { useState } from "react";
import { api, type Filter, type Project } from "../lib/api";
import { useQuery } from "../lib/store";
import { Icon } from "./Icon";
import { ErrorBox, Field } from "./ui";

interface Attached extends Filter {
  automatic: boolean;
  targetProject?: string;
  targetFolder?: string;
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
 * The filters this project uses, and where each of them sends things.
 *
 * The filter itself is only the pattern — "folders called Grundlagen-something".
 * Where they go is set here, so the same filter serves two projects that want
 * different destinations.
 */
export default function ProjectFilters({ project }: { project: Project }) {
  const mine = useQuery<{ filters: Attached[] }>(`/api/projects/${project.id}/filters`);
  const all = useQuery<{ filters: Filter[] }>("/api/filters");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const [error, setError] = useState<Error | null>(null);
  const [plan, setPlan] = useState<Step[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState({ filter: "", target: "", folder: "" });

  const attached = mine.data?.filters ?? [];
  const available = (all.data?.filters ?? []).filter((f) => !attached.some((a) => a.id === f.id));
  const address = (p: Project) => `${p.groupSlug || "ungrouped"}/${p.slug}`;

  const attach = async (body: Record<string, unknown>) => {
    setError(null);
    try {
      await api(`/api/projects/${project.id}/filters`, { body });
      mine.reload();
      setAdding({ filter: "", target: "", folder: "" });
    } catch (err) {
      setError(err as Error);
    }
  };

  const run = async (apply: boolean) => {
    setBusy(true);
    setError(null);
    try {
      const res = await api<{ steps: Step[] }>(`/api/projects/${project.id}/filter`, { body: { apply } });
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
        <div className="list" style={{ marginBottom: 10 }}>
          {attached.map((f) => (
            <div key={f.id} className="list-row">
              <Icon name="search" size={15} />
              <span className="grow">
                {f.title}
                <span className="meta">
                  {" → "}
                  {[f.targetProject, f.targetFolder].filter(Boolean).join("/") || "as the rules say"}
                </span>
              </span>
              <label className="check" style={{ margin: 0 }}>
                <input
                  type="checkbox"
                  checked={f.automatic}
                  onChange={(e) =>
                    attach({
                      filter: f.id,
                      automatic: e.target.checked,
                      target: f.targetProject,
                      folder: f.targetFolder,
                    })
                  }
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
      ) : null}

      {available.length ? (
        <div className="row" style={{ alignItems: "end" }}>
          <Field label="Filter">
            <select
              value={adding.filter}
              onChange={(e) => setAdding({ ...adding, filter: e.target.value })}
            >
              <option value="">— pick one —</option>
              {available.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.title}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Into">
            <select value={adding.target} onChange={(e) => setAdding({ ...adding, target: e.target.value })}>
              <option value="">this project</option>
              {(projects.data?.projects ?? [])
                .filter((p) => p.id !== project.id)
                .map((p) => (
                  <option key={p.id} value={address(p)}>
                    {address(p)}
                  </option>
                ))}
            </select>
          </Field>
          <Field label="Folder">
            <input
              value={adding.folder}
              onChange={(e) => setAdding({ ...adding, folder: e.target.value })}
              placeholder="optional"
            />
          </Field>
          <button
            className="btn"
            disabled={!adding.filter}
            onClick={() => attach({ filter: adding.filter, target: adding.target, folder: adding.folder })}
          >
            Add
          </button>
        </div>
      ) : null}

      {attached.length ? (
        <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
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
            Nothing matches.
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
