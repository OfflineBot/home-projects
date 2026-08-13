import { Copyable, ErrorBox, Spinner } from "../components/ui";
import { Icon } from "../components/Icon";
import { type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/** The published folder, and whether it actually holds a site. */
export default function SiteView({ project }: { project: Project; reload: () => void }) {
  const { data, error, loading, reload } = useQuery<{
    siteRoot: string;
    url: string;
    hasIndex: boolean;
    published: boolean;
    note?: string;
  }>(`/api/projects/${project.id}/site/status`);

  return (
    <div>
      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <>
          <div className="grid-2">
            <div className="tile">
              <h3>
                <Icon name="globe" size={16} /> Address
              </h3>
              <Copyable value={data.url} />
              {data.published && data.hasIndex ? (
                <a className="btn primary" href={data.url} target="_blank" rel="noreferrer">
                  Open
                </a>
              ) : null}
              {data.note ? <div className="warning">{data.note}</div> : null}
            </div>
            <div className="tile">
              <h3>Served folder</h3>
              <div className="stat">{data.siteRoot || "—"}</div>
              <div className="sub">
                Publishing does not make the project public: only this folder is served, the rest stays as
                configured. Pick the folder in the project's settings.
              </div>
            </div>
          </div>

          {data.published ? (
            <div style={{ marginTop: 18 }}>
              <h3>Preview</h3>
              <iframe
                title="preview"
                src={data.url}
                style={{
                  width: "100%",
                  height: 460,
                  border: "1px solid var(--ctp-surface1)",
                  borderRadius: "var(--radius)",
                  background: "var(--ctp-crust)",
                }}
              />
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
