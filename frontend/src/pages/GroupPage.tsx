import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import CreateProject from "../components/CreateProject";
import GroupSettings from "../components/GroupSettings";
import Graph from "../components/Graph";
import { Icon } from "../components/Icon";
import ProjectSettings from "../components/ProjectSettings";
import { Copyable, Empty, ErrorBox, Field, Menu, Modal, Spinner } from "../components/ui";
import { ApiError, api, type Group, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

export default function GroupPage() {
  const { group: slug } = useParams();
  const session = useSession();
  const navigate = useNavigate();
  const { data, error, loading, reload } = useQuery<{ group: Group; projects: Project[] }>(
    slug ? `/api/groups/${slug}` : null,
  );
  const [creating, setCreating] = useState(false);
  const [groupSettings, setGroupSettings] = useState(false);
  const [projectSettings, setProjectSettings] = useState<Project | null>(null);
  const [showGit, setShowGit] = useState(false);

  // A protected group asks for its password instead of pretending it is missing.
  if (error instanceof ApiError && error.needsPassword) {
    return <UnlockGroup slug={slug!} onUnlocked={reload} />;
  }

  return (
    <>
      <div className="page-head">
        <div>
          <div style={{ color: "var(--ctp-subtext0)", fontSize: 13 }}>
            <Link to="/groups">Groups</Link>
          </div>
          <h1 style={{ display: "flex", alignItems: "center", gap: 10 }}>
            {data ? <Icon name={data.group.icon} size={22} /> : null}
            {data?.group.title ?? slug}
          </h1>
          <p>{data?.group.description}</p>
        </div>
        <div className="head-actions">
          <button className="btn" onClick={() => setShowGit(true)}>
            <Icon name="git" size={16} /> Repository
          </button>
          {session.user && data ? (
            <>
              <button className="btn" onClick={() => setGroupSettings(true)}>
                <Icon name="settings" size={16} /> Settings
              </button>
              <button className="btn primary" onClick={() => setCreating(true)}>
                <Icon name="plus" size={16} /> New project
              </button>
            </>
          ) : null}
        </div>
      </div>

      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {data?.group.readOnly ? (
        <div className="warning">
          <Icon name="lock" size={15} /> Read-only — not through the UI, not through the API, not with git push.
        </div>
      ) : null}

      {data && data.projects.length === 0 ? (
        <Empty icon="box">No projects yet.</Empty>
      ) : null}

      <div className="tiles">
        {data?.projects.map((p) => (
          <Link
            key={p.id}
            to={`/groups/${data.group.slug}/${p.slug}`}
            className="tile"
            style={{ ["--tile-color" as string]: colorVar(p.color || data.group.color) }}
          >
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name={p.icon || "box"} />
              </span>
              <div style={{ minWidth: 0 }}>
                <h3>{p.title}</h3>
                <div className="sub">{p.capabilities.join(" · ") || "files"}</div>
              </div>
            </div>
            {p.locked ? <div className="sub">Locked — open it to enter the password</div> : null}
            {p.description ? <div className="sub">{p.description}</div> : null}
            <div className="tile-foot">
              {p.locked ? (
                <span className="badge warn">
                  <Icon name="lock" size={12} /> locked
                </span>
              ) : (p.effectiveVisibility ?? p.visibility) !== "private" ? (
                <span className="badge">
                  <Icon name={(p.effectiveVisibility ?? p.visibility) === "public" ? "eye" : "lock"} size={12} />{" "}
                  {p.effectiveVisibility ?? p.visibility}
                </span>
              ) : null}
              {p.readOnly ? <span className="badge warn">read-only</span> : null}
              {p.gitTracked ? <span className="badge">tracked</span> : null}
              {p.siteRoot ? <span className="badge good">site</span> : null}
              {p.anonWrite ? <span className="badge">visitors may write</span> : null}
            </div>
            {session.user ? (
              <div className="tile-menu">
                <Menu
                  label={`Settings for ${p.title}`}
                  items={[
                    { label: "Settings", icon: "settings", onClick: () => setProjectSettings(p) },
                    {
                      label: "Download ZIP",
                      icon: "download",
                      onClick: () => (location.href = `/api/projects/${p.id}/download`),
                    },
                    ...(p.siteUrl
                      ? [{ label: "Open site", icon: "globe" as const, onClick: () => window.open(p.siteUrl, "_blank") }]
                      : []),
                  ]}
                />
              </div>
            ) : null}
          </Link>
        ))}
      </div>

      {data ? <Graph group={data.group.slug} /> : null}

      {creating && data ? (
        <CreateProject
          groupId={data.group.id}
          groupTitle={data.group.title}
          onClose={() => setCreating(false)}
          onCreated={(p) => navigate(`/groups/${data.group.slug}/${p.slug}`)}
        />
      ) : null}

      {groupSettings && data ? (
        <GroupSettings
          group={data.group}
          projects={data.projects}
          onClose={() => setGroupSettings(false)}
          onChanged={(g) => {
            setGroupSettings(false);
            if (g.slug !== data.group.slug) navigate(`/groups/${g.slug}`);
            else reload();
          }}
          onDeleted={() => navigate("/groups")}
        />
      ) : null}

      {projectSettings ? (
        <ProjectSettings
          project={projectSettings}
          onClose={() => setProjectSettings(null)}
          onChanged={() => {
            setProjectSettings(null);
            reload();
          }}
          onDeleted={reload}
        />
      ) : null}

      {showGit && data ? <GitPanel group={data.group} onClose={() => setShowGit(false)} /> : null}
    </>
  );
}

function GitPanel({ group, onClose }: { group: Group; onClose: () => void }) {
  const { data, error } = useQuery<{
    cloneUrl: string;
    sshCloneUrl?: string;
    branches: string[];
    commits: { short: string; message: string; author: string; at: string }[];
    hint: string;
    sshHint?: string;
  }>(`/api/groups/${group.slug}/git`);

  return (
    <Modal title={`Repository · ${group.title}`} onClose={onClose}>
      <ErrorBox error={error} />
      <p style={{ marginTop: 0, color: "var(--ctp-subtext0)" }}>
        The group carries the repository; every project is a branch inside it.
      </p>
      <Field label="Clone the whole group">
        <Copyable value={`git clone ${data?.cloneUrl ?? ""}`} />
      </Field>
      <Field label="Clone a single project">
        <Copyable value={data?.hint ?? ""} />
      </Field>
      {data?.sshCloneUrl ? (
        <Field label="Over SSH" hint="Needs a key registered under Security.">
          <Copyable value={`git clone ${data.sshCloneUrl}`} />
        </Field>
      ) : null}
      <Field label={`Branches (${data?.branches?.length ?? 0})`}>
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
          {(data?.branches ?? []).map((b) => (
            <span key={b} className="badge">
              {b}
            </span>
          ))}
        </div>
      </Field>
      {data?.commits?.length ? (
        <Field label="main">
          <div className="list">
            {data.commits.map((c) => (
              <div key={c.short} className="list-row">
                <code className="mono">{c.short}</code>
                <span className="grow">{c.message}</span>
                <span className="meta">{c.author}</span>
              </div>
            ))}
          </div>
        </Field>
      ) : null}
    </Modal>
  );
}

function UnlockGroup({ slug, onUnlocked }: { slug: string; onUnlocked: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<Error | null>(null);
  return (
    <div style={{ maxWidth: 380, margin: "10vh auto" }}>
      <h1 style={{ marginTop: 0 }}>
        <Icon name="lock" /> Protected
      </h1>
      <p style={{ color: "var(--ctp-subtext0)" }}>This group asks for a password.</p>
      <ErrorBox error={error} />
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            await api(`/api/groups/${slug}/unlock`, { body: { password } });
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
