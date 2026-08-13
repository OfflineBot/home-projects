import { Suspense, useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import FilesView from "../caps/FilesView";
import { capabilityViews } from "../caps/index";
import { Icon } from "../components/Icon";
import ProjectSettings from "../components/ProjectSettings";
import { Copyable, ErrorBox, Field, Spinner, formatDate } from "../components/ui";
import { ApiError, api, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/**
 * A project shows what it is: one tab per switched-on capability, plus files
 * and git. With no capability at all it is simply a file tree — which is the
 * point.
 */
export default function ProjectPage() {
  const { project: ref } = useParams();
  const session = useSession();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();

  const { data, error, loading, reload } = useQuery<{ project: Project; tool?: any; toolError?: string }>(
    ref ? `/api/projects/${ref}` : null,
  );
  const [settings, setSettings] = useState(false);
  const project = data?.project;

  // The tab lives in the URL, so the back button works and a link keeps the view.
  const tab = params.get("tab") ?? project?.defaultTab ?? "files";
  const setTab = (next: string) => {
    const p = new URLSearchParams(params);
    p.set("tab", next);
    p.delete("path");
    p.delete("file");
    setParams(p);
  };

  useEffect(() => {
    if (project && !params.get("tab")) {
      const p = new URLSearchParams(params);
      p.set("tab", project.defaultTab || "files");
      setParams(p, { replace: true });
    }
  }, [project, params, setParams]);

  if (error instanceof ApiError && error.needsPassword) {
    return <UnlockProject slug={ref!} onUnlocked={reload} />;
  }
  if (loading && !project) return <Spinner />;
  if (!project) return <ErrorBox error={error} onRetry={reload} />;

  const tabs = [
    ...project.capabilities
      .filter((name) => capabilityViews[name])
      .map((name) => ({ key: name, title: capabilityViews[name].tab, icon: capabilityViews[name].icon })),
    { key: "files", title: "Files", icon: "folder" },
    { key: "git", title: "Version", icon: "git" },
  ];
  if (!tabs.some((t) => t.key === tab)) tabs.push({ key: tab, title: tab, icon: "box" });

  const View = capabilityViews[tab]?.component;

  return (
    <>
      <div className="page-head">
        <div>
          <div style={{ color: "var(--ctp-subtext0)", fontSize: 13 }}>
            <Link to="/groups">Groups</Link>
            {project.groupSlug ? (
              <>
                {" · "}
                <Link to={`/groups/${project.groupSlug}`}>{project.groupTitle}</Link>
              </>
            ) : (
              " · Ungrouped"
            )}
          </div>
          <h1 style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <Icon name={project.icon || "box"} size={22} />
            {project.title}
          </h1>
          <p>{project.description}</p>
        </div>
        <div className="head-actions">
          {project.siteUrl ? (
            <a className="btn" href={project.siteUrl} target="_blank" rel="noreferrer">
              <Icon name="globe" size={16} /> Open site
            </a>
          ) : null}
          {session.user ? (
            <button className="btn" onClick={() => setSettings(true)}>
              <Icon name="settings" size={16} /> Settings
            </button>
          ) : null}
        </div>
      </div>

      {project.readOnly ? (
        <div className="warning">
          <Icon name="lock" size={15} /> This project is read-only — through the UI, the API and{" "}
          <code className="mono">git push</code> alike.
        </div>
      ) : null}
      {data.toolError ? (
        <div className="warning">
          <strong>project.yaml:</strong> {data.toolError} — the project stays usable as a file tree.
        </div>
      ) : null}

      <div className="tabs">
        {tabs.map((t) => (
          <button key={t.key} className={t.key === tab ? "tab active" : "tab"} onClick={() => setTab(t.key)}>
            <Icon name={t.icon} size={15} /> {t.title}
          </button>
        ))}
      </div>

      <Suspense fallback={<Spinner />}>
        {tab === "files" ? (
          <FilesView project={project} reload={reload} />
        ) : tab === "git" ? (
          <GitTab project={project} />
        ) : View ? (
          <View project={project} reload={reload} />
        ) : (
          <FilesView project={project} reload={reload} />
        )}
      </Suspense>

      {data.tool ? <ToolPanel project={project} spec={data.tool} /> : null}

      {settings ? (
        <ProjectSettings
          project={project}
          onClose={() => setSettings(false)}
          onChanged={(p) => {
            setSettings(false);
            if (p.slug !== project.slug || p.groupSlug !== project.groupSlug) {
              navigate(p.groupSlug ? `/groups/${p.groupSlug}/${p.slug}` : `/p/${p.id}`);
            } else {
              reload();
            }
          }}
          onDeleted={() => navigate(project.groupSlug ? `/groups/${project.groupSlug}` : "/groups")}
        />
      ) : null}
    </>
  );
}

/** What project.yaml declares: the project's own variables and buttons. */
function ToolPanel({ project, spec }: { project: Project; spec: any }) {
  const { data, reload } = useQuery<{ variables: any[] }>(`/api/projects/${project.id}/variables`);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const actions: { name: string; run: string }[] = spec.actions ?? [];
  if (!actions.length && !spec.variables) return null;

  return (
    <div style={{ marginTop: 28 }}>
      <h2 style={{ fontSize: 17 }}>
        <Icon name="wrench" size={16} /> This project as a tool
      </h2>
      <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
        Declared in <code className="mono">project.yaml</code> — versioned and copied along with the project.
      </p>
      <ErrorBox error={error} />
      <div className="grid-2">
        <div className="tile">
          <h3>Variables</h3>
          <table className="data">
            <tbody>
              {(data?.variables ?? []).map((v) => (
                <tr key={v.name}>
                  <td>{v.name}</td>
                  <td>
                    {v.error ? (
                      <span style={{ color: "var(--ctp-red)" }}>{v.error}</span>
                    ) : (
                      <strong>{formatValue(v.value)}</strong>
                    )}{" "}
                    <span style={{ color: "var(--ctp-subtext0)" }}>{v.unit}</span>
                  </td>
                  <td className="meta">{formatDate(v.updatedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <button className="btn small" onClick={reload}>
            <Icon name="refresh" size={14} /> Refresh
          </button>
        </div>
        {actions.length ? (
          <div className="tile">
            <h3>Buttons</h3>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
              {actions.map((a) => (
                <button
                  key={a.name}
                  className="btn"
                  disabled={busy === a.name}
                  onClick={async () => {
                    setBusy(a.name);
                    setError(null);
                    try {
                      await api(`/api/projects/${project.id}/automation/rules/${encodeURIComponent(a.name)}/run`, {
                        method: "POST",
                      });
                    } catch (err) {
                      setError(err as Error);
                    } finally {
                      setBusy(null);
                      reload();
                    }
                  }}
                >
                  <Icon name="play" size={14} /> {a.name}
                </button>
              ))}
            </div>
            <div className="sub">
              A button runs the action it names. The same buttons can sit on the dashboard.
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function formatValue(v: any) {
  if (v === null || v === undefined) return "—";
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (Array.isArray(v)) return `${v.length} entries`;
  if (typeof v === "object") return JSON.stringify(v).slice(0, 60);
  return String(v);
}

function GitTab({ project }: { project: Project }) {
  const { data, error, reload } = useQuery<{
    branch: string;
    repository: string;
    cloneCommand: string;
    sshCloneCommand?: string;
    tracked: boolean;
    hasHistory: boolean;
    commits: { short: string; message: string; author: string; at: string }[];
  }>(`/api/projects/${project.id}/git`);
  const [message, setMessage] = useState("");
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [commitError, setCommitError] = useState<Error | null>(null);

  return (
    <div className="grid-2">
      <div>
        <ErrorBox error={commitError ?? error} onRetry={reload} />
        <div className="tile">
          <h3>Commit</h3>
          <p className="sub" style={{ marginTop: 0 }}>
            Versioning is a decision, not an automatism: the branch exists, commits happen when you say so.
            {data?.tracked ? " This project also commits automatically on every change." : ""}
          </p>
          <Field label="Message">
            <input
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder={`Update ${project.title}`}
            />
          </Field>
          <button
            className="btn primary"
            disabled={busy || project.readOnly}
            onClick={async () => {
              setBusy(true);
              setCommitError(null);
              try {
                const result = await api<{ changed: boolean; message?: string }>(
                  `/api/projects/${project.id}/git/commit`,
                  { method: "POST", body: { message } },
                );
                setNote(result.changed ? "Committed." : (result.message ?? "Nothing has changed."));
                setMessage("");
                reload();
              } catch (err) {
                setCommitError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            <Icon name="git" size={15} /> Commit now
          </button>
          {note ? <div className="notice" style={{ marginTop: 12 }}>{note}</div> : null}
        </div>

        <div className="tile" style={{ marginTop: 16 }}>
          <h3>Clone</h3>
          <Copyable value={data?.cloneCommand ?? ""} />
          {data?.sshCloneCommand ? (
            <>
              <div className="sub" style={{ marginTop: 8 }}>or over SSH, with a registered key:</div>
              <Copyable value={data.sshCloneCommand} />
            </>
          ) : null}
          <div className="sub">
            The project is the branch <code className="mono">{data?.branch}</code> in its group's repository.
            A markdown project stays in sync with an Obsidian vault this way.
          </div>
        </div>
      </div>

      <div>
        <h3 style={{ marginTop: 0 }}>History</h3>
        {data?.commits?.length ? (
          <div className="list">
            {data.commits.map((c) => (
              <div key={c.short} className="list-row">
                <code className="mono">{c.short}</code>
                <span className="grow">{c.message}</span>
                <span className="meta">{c.author}</span>
                <span className="meta">{formatDate(c.at)}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="empty">No commits yet. The branch appears with the first one.</div>
        )}
      </div>
    </div>
  );
}

function UnlockProject({ slug, onUnlocked }: { slug: string; onUnlocked: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<Error | null>(null);
  return (
    <div style={{ maxWidth: 380, margin: "10vh auto" }}>
      <h1 style={{ marginTop: 0 }}>
        <Icon name="lock" /> Protected
      </h1>
      <p style={{ color: "var(--ctp-subtext0)" }}>This project asks for a password.</p>
      <ErrorBox error={error} />
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            await api(`/api/projects/${slug}/unlock`, { body: { password } });
            onUnlocked();
          } catch (err) {
            setError(err as Error);
          }
        }}
      >
        <Field label="Password">
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
        </Field>
        <button className="btn primary" style={{ width: "100%" }}>
          Open
        </button>
      </form>
    </div>
  );
}
