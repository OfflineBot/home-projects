import { Link } from "react-router-dom";
import { Icon } from "../../components/Icon";
import { Spinner } from "../../components/ui";
import { useQuery } from "../../lib/store";
import type { CardProps } from "../../components/board/cards";

/** The newest saved links of a project, to click straight through. */
export default function LinksCard({ options }: CardProps) {
  const project = String(options.projectId ?? "");
  const only = String(options.tag ?? "");
  const { data, loading } = useQuery<{
    links: { id: string; url: string; title: string; note?: string; tags?: string[]; done?: boolean }[];
  }>(project ? `/api/projects/${project}/links` : null);

  if (!project) return <div className="meta">This card has no project yet.</div>;
  if (loading && !data) return <Spinner />;
  const links = (data?.links ?? [])
    .filter((l) => !l.done && (!only || (l.tags ?? []).includes(only)))
    .slice(0, 12);

  return (
    <div className="card-links">
      <div className="meta">{options.title || "Saved links"}</div>
      {links.map((l) => (
        <a key={l.id} className="card-link" href={l.url} target="_blank" rel="noreferrer">
          <Icon name="link" size={14} /> {l.title}
        </a>
      ))}
      {links.length === 0 ? <span className="meta">Nothing kept yet.</span> : null}
      <Link className="meta" to={`/p/${project}?tab=links`}>
        all of them
      </Link>
    </div>
  );
}
