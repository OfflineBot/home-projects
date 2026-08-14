import { useEffect, useState } from "react";
import { Icon } from "../components/Icon";
import { ErrorBox, Field, Spinner, formatDate } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

interface Module {
  name: string;
  grade?: number;
  gradeText?: string;
  credits: number;
  semester?: string;
  status?: string;
}

/** Grades: the table and the average, straight out of grades.json. */
export default function GradesView({ project }: { project: Project; reload: () => void }) {
  const { data, error, loading, reload } = useQuery<{
    modules: Module[];
    average: number;
    credits: number;
    counted: number;
    source?: string;
    fetchedAt?: string;
  }>(`/api/projects/${project.id}/grades/`);

  const [rows, setRows] = useState<Module[] | null>(null);
  const [saveError, setSaveError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const modules = rows ?? data?.modules ?? [];

  useEffect(() => setRows(null), [data]);

  const save = async (next: Module[]) => {
    setRows(next);
    setBusy(true);
    setSaveError(null);
    try {
      await api(`/api/projects/${project.id}/grades/`, { method: "PUT", body: { modules: next } });
      reload();
    } catch (err) {
      setSaveError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <ErrorBox error={saveError ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      <div className="grid-2" style={{ marginBottom: 18 }}>
        <div className="tile">
          <div className="sub">Average</div>
          <div className="stat">
            {data?.average ? data.average.toFixed(2) : "—"}
            <span className="unit">over {data?.counted ?? 0} graded modules</span>
          </div>
        </div>
        <div className="tile">
          <div className="sub">Credits</div>
          <div className="stat">
            {data?.credits ?? 0}
            <span className="unit">ECTS</span>
          </div>
        </div>
      </div>

      {data?.source ? (
        <p className="meta" style={{ color: "var(--ctp-subtext0)" }}>
          from {data.source}, fetched {formatDate(data.fetchedAt)} — a scheduler run overwrites this file.
        </p>
      ) : null}

      <table className="data">
        <thead>
          <tr>
            <th>Module</th>
            <th style={{ width: 110 }}>Grade</th>
            <th style={{ width: 90 }}>ECTS</th>
            <th style={{ width: 140 }}>Semester</th>
            <th style={{ width: 120 }}>Status</th>
            <th style={{ width: 40 }} />
          </tr>
        </thead>
        <tbody>
          {modules.map((m, i) => (
            <tr key={i}>
              <td>
                <input
                  value={m.name}
                  readOnly={project.readOnly}
                  onChange={(e) => setRows(modules.map((x, j) => (i === j ? { ...x, name: e.target.value } : x)))}
                  onBlur={() => rows && save(rows)}
                />
              </td>
              <td>
                <input
                  type="number"
                  step="0.1"
                  value={m.grade ?? ""}
                  readOnly={project.readOnly}
                  onChange={(e) =>
                    setRows(modules.map((x, j) => (i === j ? { ...x, grade: Number(e.target.value) } : x)))
                  }
                  onBlur={() => rows && save(rows)}
                />
              </td>
              <td>
                <input
                  type="number"
                  value={m.credits ?? 0}
                  readOnly={project.readOnly}
                  onChange={(e) =>
                    setRows(modules.map((x, j) => (i === j ? { ...x, credits: Number(e.target.value) } : x)))
                  }
                  onBlur={() => rows && save(rows)}
                />
              </td>
              <td>
                <input
                  value={m.semester ?? ""}
                  readOnly={project.readOnly}
                  onChange={(e) => setRows(modules.map((x, j) => (i === j ? { ...x, semester: e.target.value } : x)))}
                  onBlur={() => rows && save(rows)}
                />
              </td>
              <td>
                <span className={`badge ${m.status === "passed" ? "good" : m.status === "failed" ? "bad" : ""}`}>
                  {m.status || "—"}
                </span>
              </td>
              <td>
                {!project.readOnly ? (
                  <button
                    className="btn ghost icon"
                    onClick={() => save(modules.filter((_, j) => j !== i))}
                    aria-label="Remove"
                  >
                    <Icon name="trash" size={15} />
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {!project.readOnly ? (
        <button
          className="btn"
          style={{ marginTop: 12 }}
          disabled={busy}
          onClick={() => save([...modules, { name: "", credits: 5, status: "pending" }])}
        >
          <Icon name="plus" size={15} /> Add a module
        </button>
      ) : null}

      <Field label="" hint="Stored as grades.json.">
        <span />
      </Field>
    </div>
  );
}
