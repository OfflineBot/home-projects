import { Link } from "react-router-dom";
import { Icon } from "../components/Icon";
import { ErrorBox, Spinner } from "../components/ui";
import { type Group, type Link as LinkRow, type Project } from "../lib/api";
import { useQuery } from "../lib/store";
import { colorVar } from "../lib/theme";

/** A visual map: which projects hang in which groups, and which links run between them. */
export default function Structure() {
  const { data, error, loading, reload } = useQuery<{
    groups: Group[];
    projects: Project[];
    links: LinkRow[];
  }>("/api/structure");

  const ungrouped = (data?.projects ?? []).filter((p) => !p.groupId);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Structure</h1>
          <p>Everything at a glance: groups, their projects, and the links between them.</p>
        </div>
      </div>
      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      <div className="tiles" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))" }}>
        {data?.groups.map((g) => {
          const projects = data.projects.filter((p) => p.groupId === g.id);
          return (
            <div key={g.id} className="tile" style={{ ["--tile-color" as string]: colorVar(g.color) }}>
              <div className="tile-top">
                <span className="tile-icon">
                  <Icon name={g.icon} />
                </span>
                <div>
                  <h3>
                    <Link to={`/groups/${g.slug}`}>{g.title}</Link>
                  </h3>
                  <div className="sub">{projects.length} projects</div>
                </div>
              </div>
              <div className="list" style={{ background: "transparent", border: "none" }}>
                {projects.map((p) => (
                  <Link
                    key={p.id}
                    to={`/groups/${g.slug}/${p.slug}`}
                    className="list-row"
                    style={{ padding: "6px 4px" }}
                  >
                    <Icon name={p.icon || "box"} size={14} />
                    <span className="grow">{p.title}</span>
                    {p.archived ? <span className="badge">archived</span> : null}
                    {p.capabilities.length ? <span className="meta">{p.capabilities.join(" · ")}</span> : null}
                  </Link>
                ))}
                {projects.length === 0 ? <span className="meta">empty</span> : null}
              </div>
            </div>
          );
        })}

        {ungrouped.length ? (
          <div className="tile">
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name="box" />
              </span>
              <h3>Ungrouped</h3>
            </div>
            <div className="list" style={{ background: "transparent", border: "none" }}>
              {ungrouped.map((p) => (
                <Link key={p.id} to={`/p/${p.id}`} className="list-row" style={{ padding: "6px 4px" }}>
                  <Icon name={p.icon || "box"} size={14} />
                  <span className="grow">{p.title}</span>
                </Link>
              ))}
            </div>
          </div>
        ) : null}
      </div>

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Links</h2>
      {data?.links.length ? (
        <div className="list">
          {data.links.map((l) => (
            <div key={l.id} className="list-row">
              <Icon name="link" size={15} />
              <span className="grow">
                <strong>{l.sourceSlug}</strong>:{l.sourcePath} → <strong>{l.targetSlug}</strong>:{l.targetPath}
              </span>
              <span className="badge">{l.kind}</span>
            </div>
          ))}
        </div>
      ) : (
        <p style={{ color: "var(--ctp-subtext0)" }}>
          No links yet. A link shows the same content in a second place — without copying it.
        </p>
      )}
    </>
  );
}
