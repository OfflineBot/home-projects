import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import CreateProject from "../components/CreateProject";
import GroupSettings from "../components/GroupSettings";
import Graph from "../components/Graph";
import { Icon } from "../components/Icon";
import ProjectSettings from "../components/ProjectSettings";
import { Copyable, Empty, ErrorBox, Field, Fold, formatDate, Menu, Modal, Spinner } from "../components/ui";
import { ApiError, api, type Group, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import Board from "../components/board/Board";
import { markdownToHtml } from "../lib/markdown";
import { colorVar } from "../lib/theme";

export default function GroupPage() {
  const { group: slug } = useParams();
  const session = useSession();
  const navigate = useNavigate();
  const { data, error, loading, reload } = useQuery<{ group: Group; projects: Project[] }>(
    slug ? `/api/groups/${slug}` : null,
  );
  const [creating, setCreating] = useState(false);
  // Board or projects. The board comes first once there is anything on it —
  // that is the page somebody arranged, and the list is always one click away.
  const [page, setPage] = useState<"board" | "projects">("board");
  const readme = useQuery<{ text: string }>(slug ? `/api/groups/${slug}/readme` : null);
  const [shut, setShut] = useState<Record<string, boolean>>({});
  const fold = (folder: string) => setShut((s) => ({ ...s, [folder]: !s[folder] }));
  const [groupSettings, setGroupSettings] = useState(false);
  const [projectSettings, setProjectSettings] = useState<Project | null>(null);
  // What the files in this group say is there but nobody switched on.
  const detect = useQuery<{
    projects: { project: string; projectId: string; capabilities: { name: string; title: string; icon: string; matched: string[] }[] }[];
  }>(`/api/groups/${slug}/detect`);
  const missing = detect.data?.projects ?? [];
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

      <div className="page-tabs">
        <button className={page === "board" ? "page-tab on" : "page-tab"} onClick={() => setPage("board")}>
          <Icon name="grid" size={15} /> Board
        </button>
        <button
          className={page === "projects" ? "page-tab on" : "page-tab"}
          onClick={() => setPage("projects")}
        >
          <Icon name="box" size={15} /> Projects
          <span className="meta">{data?.projects.length ?? 0}</span>
        </button>
      </div>

      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {page === "projects" && readme.data?.text ? (
        <div className="prose group-readme" dangerouslySetInnerHTML={{ __html: markdownToHtml(readme.data.text) }} />
      ) : null}

      {page === "board" && slug ? (
        <Board
          group={slug}
          emptyNote="This group has no board yet — put the things you use every day on it."
        />
      ) : null}

      {data?.group.readOnly ? (
        <div className="warning">
          <Icon name="lock" size={15} /> Read-only — not through the UI, not through the API, not with git push.
        </div>
      ) : null}

      {page === "projects" && data && data.projects.length === 0 ? (
        <Empty icon="box">No projects yet.</Empty>
      ) : null}

      {page === "projects"
        ? folders(data?.projects ?? []).map(([folder, list]) => (
            <div key={folder || "—"} style={{ marginBottom: 18 }}>
              {folder ? (
                <button className="folder-head" onClick={() => fold(folder)}>
                  <Icon name={shut[folder] ? "chevronRight" : "chevronDown"} size={13} />
                  <Icon name="folder" size={14} /> {folder}
                  <span className="meta">{list.length}</span>
                </button>
              ) : null}
              {shut[folder] ? null : (
                <ProjectTiles
                  group={data!.group}
                  projects={list}
                  session={session}
                  onSettings={setProjectSettings}
                />
              )}
            </div>
          ))
        : null}

      {missing.length ? (
        <div className="notice" style={{ marginTop: 18 }}>
          <strong>Already in the files, not switched on:</strong>
          <div className="list" style={{ background: "transparent", border: "none", marginTop: 6 }}>
            {missing.map((row) => (
              <div key={row.projectId} className="list-row" style={{ padding: "6px 0" }}>
                <span className="grow mono">{row.project}</span>
                {row.capabilities.map((cap) => (
                  <button
                    key={cap.name}
                    className="btn small"
                    title={cap.matched.join(", ")}
                    onClick={async () => {
                      const p = data?.projects.find((x) => x.id === row.projectId);
                      if (!p) return;
                      await api(`/api/projects/${row.projectId}`, {
                        method: "PATCH",
                        body: { capabilities: [...p.capabilities, cap.name] },
                      });
                      reload();
                      detect.reload();
                    }}
                  >
                    <Icon name={cap.icon} size={13} /> {cap.title}
                  </button>
                ))}
              </div>
            ))}
          </div>
        </div>
      ) : null}

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
  // Which branch's history is on screen — main is the group's own, every other
  // one is a project.
  const [branch, setBranch] = useState("main");
  const [patch, setPatch] = useState<{ hash: string; patch: string } | null>(null);
  const { data, error } = useQuery<{
    cloneUrl: string;
    sshCloneUrl?: string;
    branches: string[];
    commits: { short: string; message: string; author: string; at: string }[];
    hint: string;
    sshHint?: string;
  }>(`/api/groups/${group.slug}/git?branch=${encodeURIComponent(branch)}`, [branch]);

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
        <div className="mail-chips">
          {(data?.branches ?? []).map((b) => (
            <button key={b} className={branch === b ? "chip on" : "chip"} onClick={() => setBranch(b)}>
              {b}
            </button>
          ))}
        </div>
      </Field>

      <Field label={`History of ${branch}`}>
        {data?.commits?.length ? (
          <div className="list">
            {data.commits.map((c) => (
              <button
                key={c.short}
                className="list-row"
                style={{ width: "100%", background: "none", border: "none", textAlign: "left", cursor: "pointer" }}
                onClick={async () => {
                  const answer = await api<{ hash: string; patch: string }>(
                    `/api/groups/${group.slug}/git/commit/${c.short}`,
                  );
                  setPatch(answer);
                }}
              >
                <code className="mono">{c.short}</code>
                <span className="grow">{c.message}</span>
                <span className="meta">{c.author}</span>
                <span className="meta">{formatDate(c.at)}</span>
              </button>
            ))}
          </div>
        ) : (
          <p className="meta">Nothing on this branch yet.</p>
        )}
      </Field>

      {patch ? (
        <Fold title={`What ${patch.hash} changed`} open>
          <pre className="block" style={{ maxHeight: 380, overflow: "auto" }}>{patch.patch}</pre>
        </Fold>
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

/** The projects of one folder, as tiles. */
function ProjectTiles({
  group,
  projects,
  session,
  onSettings,
}: {
  group: Group;
  projects: Project[];
  session: { user: unknown };
  onSettings: (p: Project) => void;
}) {
  return (
    <div className="tiles">
      {projects.map((p) => (
          <Link
            key={p.id}
            to={`/groups/${group.slug}/${p.slug}`}
            className="tile"
            style={{ ["--tile-color" as string]: colorVar(p.color || group.color) }}
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
                    { label: "Settings", icon: "settings", onClick: () => onSettings(p) },
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
  );
}

/** The projects, gathered under their folders. The unfiled come first. */
function folders(projects: Project[]): [string, Project[]][] {
  const out = new Map<string, Project[]>();
  for (const p of projects) {
    const key = p.folder ?? "";
    out.set(key, [...(out.get(key) ?? []), p]);
  }
  return [...out.entries()].sort(([a], [b]) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
}
