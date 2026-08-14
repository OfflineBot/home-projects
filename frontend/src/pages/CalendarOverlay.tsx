import { useState } from "react";
import { CalendarGrid } from "../caps/CalendarView";
import { ErrorBox, Spinner } from "../components/ui";
import { useQuery } from "../lib/store";

/**
 * Several calendars at once: the events of every calendar project, each in its
 * project's colour and individually switchable. Narrow it to one group, or
 * leave it across all of them.
 */
export default function CalendarOverlay() {
  const { data, error, loading } = useQuery<{
    sources: { id: string; slug: string; title: string; color: string; group: string; groupTitle: string; readOnly: boolean }[];
  }>("/api/capabilities/calendar/events?from=2000-01-01&to=2000-01-02");
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [group, setGroup] = useState("");

  const sources = (data?.sources ?? []).filter((s) => !group || s.group === group);
  const groups = Array.from(new Set((data?.sources ?? []).map((s) => s.group).filter(Boolean)));

  if (loading && !data) return <Spinner />;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Calendar</h1>
        </div>
      </div>
      <ErrorBox error={error} />

      {groups.length > 1 ? (
        <div style={{ marginBottom: 12, display: "flex", gap: 8, alignItems: "center" }}>
          <span style={{ color: "var(--ctp-subtext0)" }}>Group</span>
          <select value={group} onChange={(e) => setGroup(e.target.value)} style={{ width: "auto" }}>
            <option value="">all groups</option>
            {groups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
        </div>
      ) : null}

      {sources.length === 0 ? (
        <div className="empty">No calendar project yet. Make one with the “Calendar” preset.</div>
      ) : (
        <CalendarGrid
          sources={sources.map((s) => ({ id: s.id, title: s.title, color: s.color, readOnly: s.readOnly }))}
          endpoint={(from, to) =>
            `/api/capabilities/calendar/events?from=${from}&to=${to}${group ? `&group=${group}` : ""}`
          }
          defaultProject={sources.find((s) => !s.readOnly)?.id}
          hidden={hidden}
          onToggleSource={(id) =>
            setHidden((prev) => {
              const next = new Set(prev);
              if (next.has(id)) next.delete(id);
              else next.add(id);
              return next;
            })
          }
        />
      )}
    </>
  );
}
