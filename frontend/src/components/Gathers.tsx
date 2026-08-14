import { useState } from "react";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";
import { Icon } from "./Icon";

/**
 * The projects a project gathers.
 *
 * This is what makes a "main calendar" an ordinary project rather than a
 * feature of its own: it gathers the other calendars, and its view draws them
 * beside its own entries. Any capability that can merge can use the same list —
 * nothing here knows what a calendar is.
 */
export default function Gathers({
  project,
  onError,
}: {
  project: Project;
  onError: (error: Error) => void;
}) {
  const mine = useQuery<{ sources: Project[] }>(`/api/projects/${project.id}/sources`);
  const all = useQuery<{ projects: Project[] }>("/api/projects");
  const [busy, setBusy] = useState(false);

  const chosen = mine.data?.sources ?? [];
  const address = (p: Project) => `${p.groupSlug || "ungrouped"}/${p.slug}`;
  // Anything but itself, and anything not already gathered. A project of a
  // different kind is allowed: a view that cannot use it simply ignores it.
  const available = (all.data?.projects ?? []).filter(
    (p) => p.id !== project.id && !chosen.some((c) => c.id === p.id),
  );

  const save = async (ids: string[]) => {
    setBusy(true);
    try {
      await api(`/api/projects/${project.id}/sources`, { method: "PUT", body: { sources: ids } });
      mine.reload();
    } catch (err) {
      onError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      {chosen.length ? (
        <div className="list" style={{ marginBottom: 8 }}>
          {chosen.map((p, i) => (
            <div key={p.id} className="list-row">
              <Icon name={p.icon} size={15} />
              <span className="grow">{address(p)}</span>
              <button
                className="btn ghost icon"
                aria-label="Earlier"
                disabled={busy || i === 0}
                onClick={() => {
                  const ids = chosen.map((c) => c.id);
                  [ids[i - 1], ids[i]] = [ids[i], ids[i - 1]];
                  void save(ids);
                }}
              >
                <Icon name="chevronUp" size={14} />
              </button>
              <button
                className="btn ghost icon"
                aria-label={`Stop gathering ${p.title}`}
                disabled={busy}
                onClick={() => void save(chosen.filter((c) => c.id !== p.id).map((c) => c.id))}
              >
                <Icon name="x" size={15} />
              </button>
            </div>
          ))}
        </div>
      ) : null}
      <select
        value=""
        disabled={busy || available.length === 0}
        onChange={(e) => {
          if (!e.target.value) return;
          void save([...chosen.map((c) => c.id), e.target.value]);
        }}
      >
        <option value="">{available.length ? "— gather another project —" : "nothing else to gather"}</option>
        {available.map((p) => (
          <option key={p.id} value={p.id}>
            {address(p)}
          </option>
        ))}
      </select>
    </div>
  );
}
