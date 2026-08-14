import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Spinner, formatDate } from "../components/ui";
import { type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/**
 * Grades, read.
 *
 * Nothing here is editable: what a certificate says is not a field to be typed
 * into. The truth is grades.json, and it gets there from Dualis or through git;
 * this view only arranges it — newest semester first, with the parts of a
 * subject folded under it and the terms behind you folded away, each still
 * showing its average.
 */

interface Module {
  id?: string;
  name: string;
  grade?: number;
  gradeText?: string;
  credits: number;
  semester?: string;
  status?: string;
  partOf?: string;
  parts?: Module[];
  computed?: boolean;
}

interface Term {
  name: string;
  modules: Module[];
  average: number;
  credits: number;
}

export default function GradesView({ project }: { project: Project; reload: () => void }) {
  const { data, error, loading, reload } = useQuery<{
    terms: Term[];
    average: number;
    credits: number;
    counted: number;
    source?: string;
    fetchedAt?: string;
  }>(`/api/projects/${project.id}/grades`);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [shut, setShut] = useState<Record<string, boolean>>({});

  const terms = data?.terms ?? [];
  // The newest term is open; the ones behind it are folded, because a closed
  // one still shows the number you came for.
  const folded = (name: string, i: number) => (name in shut ? shut[name] : i > 0);

  return (
    <div>
      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {data ? (
        <div className="stat-row">
          <Stat icon="award" value={data.average ? data.average.toFixed(2) : "–"} label="Average" />
          <Stat icon="graduation" value={String(data.credits ?? 0)} label="Credits" />
          <Stat icon="notebook" value={String(data.counted ?? 0)} label="Graded" />
        </div>
      ) : null}

      {data && terms.length === 0 ? <Empty icon="award">Nothing in grades.json yet.</Empty> : null}

      {terms.map((term, ti) => (
        <section key={term.name} className={folded(term.name, ti) ? "term shut" : "term"}>
          <header onClick={() => setShut({ ...shut, [term.name]: !folded(term.name, ti) })}>
            <h3>
              <Icon name={folded(term.name, ti) ? "chevronRight" : "chevronDown"} size={14} /> {term.name}
            </h3>
            <div className="meta">
              ⌀ <strong>{term.average ? term.average.toFixed(2) : "–"}</strong> · {term.credits} credits ·{" "}
              {term.modules.length} modules
            </div>
          </header>
          {folded(term.name, ti) ? null : (
          <table className="grade-table">
            <thead>
              <tr>
                <th>Module</th>
                <th className="num">Grade</th>
                <th className="num">Credits</th>
              </tr>
            </thead>
            <tbody>
              {term.modules.map((m, i) => {
                const key = term.name + ":" + (m.id || m.name) + i;
                const parts = m.parts ?? [];
                // Parts are shown, not hidden: they are the reason the grade is
                // what it is. Clicking folds them away again.
                const expanded = key in open ? open[key] : true;
                return (
                  <>
                    <tr
                      key={key}
                      className={[
                        "status-" + (m.status ?? ""),
                        parts.length ? "expandable" : "",
                        expanded ? "open" : "",
                      ].join(" ")}
                      onClick={() => parts.length && setOpen({ ...open, [key]: !expanded })}
                    >
                      <td>
                        {parts.length ? (
                          <Icon name={expanded ? "chevronDown" : "chevronRight"} size={13} />
                        ) : null}{" "}
                        {m.name}
                        {parts.length && !expanded ? (
                          <span className="meta"> · {parts.length} parts</span>
                        ) : null}
                      </td>
                      <td className="num">
                        {m.gradeText || (m.grade ? m.grade.toFixed(1) : "–")}
                        {m.computed ? <span className="meta" title="worked out from the parts"> ⌀</span> : null}
                      </td>
                      <td className="num">{m.credits || "–"}</td>
                    </tr>
                    {expanded
                      ? parts.map((part, pi) => (
                          <tr key={key + "p" + pi} className="part">
                            <td>{part.name}</td>
                            <td className="num">{part.gradeText || (part.grade ? part.grade.toFixed(1) : "–")}</td>
                            <td className="num">{part.credits || "–"}</td>
                          </tr>
                        ))
                      : null}
                  </>
                );
              })}
            </tbody>
          </table>
          )}
        </section>
      ))}

      {data?.fetchedAt ? (
        <p className="meta" style={{ marginTop: 18 }}>
          {data.source || "grades.json"} · {formatDate(data.fetchedAt)}
        </p>
      ) : null}
    </div>
  );
}

function Stat({ icon, value, label }: { icon: string; value: string; label: string }) {
  return (
    <div className="stat-tile">
      <span className="tile-icon">
        <Icon name={icon} />
      </span>
      <div>
        <div className="stat">{value}</div>
        <div className="meta">{label}</div>
      </div>
    </div>
  );
}
