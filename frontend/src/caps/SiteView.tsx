import { useState } from "react";
import { Copyable, ErrorBox, Field, Spinner, useGuarded } from "../components/ui";
import { Icon } from "../components/Icon";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

interface Status {
  siteRoot: string;
  url: string;
  hasIndex: boolean;
  published: boolean;
  note?: string;
  sourceId?: string;
  sourceTitle?: string;
  sourceSlug?: string;
  protected: boolean;
}

/**
 * A site is three decisions and no files: the address it answers at, the
 * project that holds the material, and the folder inside it. Whether a password
 * stands in front is the fourth.
 *
 * Keeping them apart is the point. What gets written, pulled and linked into is
 * a project; what is published is an address pointing at one of its folders.
 * The same folder can be published twice, and a project can be rearranged
 * without the address moving.
 */
export default function SiteView({ project, reload: reloadProject }: { project: Project; reload: () => void }) {
  const guarded = useGuarded();
  const { data, error, loading, reload } = useQuery<Status>(`/api/projects/${project.id}/site/status`);
  const projects = useQuery<{ projects: Project[] }>("/api/projects");

  const [source, setSource] = useState<string | null>(null);
  const [root, setRoot] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<Error | null>(null);

  const sourceValue = source ?? data?.sourceId ?? "";
  const rootValue = root ?? data?.siteRoot ?? "";

  const candidates = useQuery<{ candidates: string[] }>(
    `/api/projects/${project.id}/site/candidates${sourceValue ? `?source=${sourceValue}` : ""}`,
    [sourceValue],
  );

  const save = async () => {
    setBusy(true);
    setSaveError(null);
    try {
      await guarded("changing what is published", () =>
        api(`/api/projects/${project.id}`, {
          method: "PATCH",
          body: { siteSource: sourceValue, siteRoot: rootValue },
        }),
      );
      setSource(null);
      setRoot(null);
      reload();
      candidates.reload();
      reloadProject();
    } catch (err) {
      setSaveError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  const changed = (source !== null && source !== (data?.sourceId ?? "")) || (root !== null && root !== data?.siteRoot);

  return (
    <div>
      <ErrorBox error={saveError ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <>
          <div className="grid-2">
            <div className="tile">
              <h3>
                <Icon name="globe" size={16} /> Address
              </h3>
              <Copyable value={data.url} />
              <div className="sub">{data.protected ? "password protected" : "open to anyone"}</div>
              {data.published && data.hasIndex ? (
                <a className="btn primary" href={data.url} target="_blank" rel="noreferrer">
                  Open
                </a>
              ) : null}
              {data.note ? <div className="warning">{data.note}</div> : null}
            </div>

            <div className="tile">
              <h3>What it serves</h3>

              <Field
                label="The project that holds the files"
                hint="This one, or any other."
              >
                <select value={sourceValue} onChange={(e) => setSource(e.target.value)}>
                  <option value="">{project.title} (this project)</option>
                  {(projects.data?.projects ?? [])
                    .filter((p) => p.id !== project.id)
                    .map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.groupSlug ? `${p.groupSlug} / ` : ""}
                        {p.title}
                      </option>
                    ))}
                </select>
              </Field>

              <Field
                label="The folder inside it"
                hint={
                  candidates.data?.candidates.length
                    ? "The folders that hold an index.html are offered; anything else can be typed."
                    : "No folder there holds an index.html yet."
                }
              >
                <input
                  list="site-candidates"
                  value={rootValue}
                  onChange={(e) => setRoot(e.target.value)}
                  placeholder="public"
                />
                <datalist id="site-candidates">
                  {(candidates.data?.candidates ?? []).map((c) => (
                    <option key={c} value={c}>
                      {c === "" ? "the project's root" : c}
                    </option>
                  ))}
                </datalist>
              </Field>

              {changed ? (
                <button className="btn primary" disabled={busy} onClick={save}>
                  {busy ? "saving…" : "Save"}
                </button>
              ) : null}

              {data.sourceTitle ? (
                <div className="sub">
                  serving {data.sourceTitle}
                  {data.siteRoot ? ` / ${data.siteRoot}` : ""}
                </div>
              ) : null}
            </div>
          </div>

          {data.published && data.hasIndex ? (
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
