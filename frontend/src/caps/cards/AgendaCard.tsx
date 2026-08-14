import { Link } from "react-router-dom";
import { Spinner } from "../../components/ui";
import { useQuery } from "../../lib/store";
import type { CardProps } from "../../components/board/cards";

/**
 * What is coming: the next entries out of one calendar project, or out of every
 * calendar there is when no project is named.
 */
export default function AgendaCard({ options }: CardProps) {
  const project = String(options.projectId ?? "");
  const days = Number(options.days ?? 14) || 14;
  const from = new Date().toISOString().slice(0, 10);
  const to = new Date(Date.now() + days * 86400_000).toISOString().slice(0, 10);
  const where = project
    ? `/api/projects/${project}/calendar/events?from=${from}&to=${to}`
    : `/api/capabilities/calendar/events?from=${from}&to=${to}`;
  const { data, loading } = useQuery<{
    events: { uid: string; start: string; summary: string; allDay: boolean; projectSlug?: string }[];
  }>(where);

  if (loading && !data) return <Spinner />;
  const events = (data?.events ?? []).slice(0, 20);

  return (
    <div className="card-agenda">
      <div className="meta">{options.title || `Next ${days} days`}</div>
      {events.length === 0 ? <div className="meta">Nothing coming up.</div> : null}
      <ul>
        {events.map((e) => (
          <li key={e.uid + e.start}>
            <span className="when">
              {new Date(e.start).toLocaleDateString(undefined, { day: "2-digit", month: "short" })}
              {e.allDay ? "" : " " + new Date(e.start).toLocaleTimeString(undefined, {
                hour: "2-digit", minute: "2-digit", hour12: false,
              })}
            </span>
            <span className="what">{e.summary}</span>
          </li>
        ))}
      </ul>
      {project ? <Link className="meta" to={`/p/${project}?tab=calendar`}>the whole calendar</Link> : null}
    </div>
  );
}
