import { useEffect, useState } from "react";
import { api, type Group, type Project } from "../lib/api";
import { useMeta } from "../lib/store";
import { colorVar } from "../lib/theme";
import { Icon } from "./Icon";
import ProjectFilters from "./ProjectFilters";
import { ConfirmDelete, Copyable, ErrorBox, Field, Modal, Section, useGuarded } from "./ui";

/** Everything the settings table in section 7 lists for a project. */
export default function ProjectSettings({
  project,
  onClose,
  onChanged,
  onDeleted,
}: {
  project: Project;
  onClose: () => void;
  onChanged: (p: Project) => void;
  onDeleted?: () => void;
}) {
  const meta = useMeta();
  const guarded = useGuarded();
  const [groups, setGroups] = useState<Group[]>([]);
  const [form, setForm] = useState({
    title: project.title,
    slug: project.slug,
    description: project.description,
    visibility: project.visibility,
    anonWrite: project.anonWrite,
    color: project.color,
    icon: project.icon,
    readOnly: project.readOnly,
    archived: project.archived,
    gitTracked: project.gitTracked,
    siteRoot: project.siteRoot ?? "",
    capabilities: [...project.capabilities],
  });
  const [password, setPassword] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [pendingMove, setPendingMove] = useState<{ groupId: string; notes: MoveNote[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [preview, setPreview] = useState<any>(null);
  const [note, setNote] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<string[]>([]);
  const [onewayKinds, setOnewayKinds] = useState<{ name: string; title: string }[]>([]);
  const [onewayKind, setOnewayKind] = useState("");
  const [onewayLink, setOnewayLink] = useState("");

  useEffect(() => {
    void api<{ groups: Group[] }>("/api/groups")
      .then((r) => setGroups(r.groups))
      .catch(() => undefined);
    if (project.capabilities.includes("site")) {
      void api<{ candidates: string[] }>(`/api/projects/${project.id}/site/candidates`)
        .then((r) => setCandidates(r.candidates))
        .catch(() => undefined);
    }
    void api<{ kinds: { name: string; title: string }[] }>(`/api/projects/${project.id}/oneway`)
      .then((r) => setOnewayKinds(r.kinds))
      .catch(() => undefined);
  }, [project.id, project.capabilities]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        title: form.title,
        description: form.description,
        color: form.color,
        icon: form.icon,
        readOnly: form.readOnly,
        archived: form.archived,
        gitTracked: form.gitTracked,
        siteRoot: form.siteRoot,
        capabilities: form.capabilities,
      };
      if (form.slug !== project.slug) body.slug = form.slug;
      if (form.visibility !== project.visibility) body.visibility = form.visibility;
      if (form.anonWrite !== project.anonWrite) body.anonWrite = form.anonWrite;
      if (password) body.password = password;

      const result = await guarded("changing who can see this project", () =>
        api<{ project: Project; pausedSchedulers?: string[] }>(`/api/projects/${project.id}`, {
          method: "PATCH",
          body,
        }),
      );
      if (result.pausedSchedulers?.length) {
        setNote(`Paused because the project is now read-only: ${result.pausedSchedulers.join(", ")}`);
        onChanged(result.project);
        setBusy(false);
        return;
      }
      onChanged(result.project);
      onClose();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  // Moving is a branch move and an address change. What that costs is asked
  // for first — an existing checkout, a group that publishes this project, a
  // name already taken over there.
  const askAboutMove = async (groupId: string) => {
    setError(null);
    setPendingMove(null);
    try {
      const target = groupId === "ungrouped" ? "ungrouped" : (groups.find((g) => g.id === groupId)?.slug ?? "");
      const impact = await api<{ notes: MoveNote[] }>(
        `/api/projects/${project.id}/move-impact?group=${encodeURIComponent(target)}`,
      );
      setPendingMove({ groupId, notes: impact.notes });
    } catch (err) {
      setError(err as Error);
    }
  };

  const move = async (groupId: string) => {
    setError(null);
    try {
      const result = await api<{ project: Project }>(`/api/projects/${project.id}/move`, {
        method: "POST",
        body: { groupId },
      });
      setPendingMove(null);
      onChanged(result.project);
    } catch (err) {
      setError(err as Error);
    }
  };

  // Emptying keeps the project, its address, its schedulers and every link —
  // and removes the contents. It asks for the name and for the password,
  // because a project that is not git-tracked has no history to come back from.
  if (clearing) {
    return (
      <Modal
        title={`Empty ${project.title}`}
        onClose={() => setClearing(false)}
        footer={
          <>
            <a className="btn" href={`/api/projects/${project.id}/download`}>
              <Icon name="download" size={15} /> Download first
            </a>
            <div style={{ flex: 1 }} />
            <button className="btn" onClick={() => setClearing(false)}>
              Cancel
            </button>
            <button
              className="btn danger"
              disabled={busy || confirmText.trim().toLowerCase() !== project.title.toLowerCase()}
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  const res = await guarded(`emptying ${project.title}`, () =>
                    api<{ removed: number; filesBefore: number }>(`/api/projects/${project.id}/clear`, {
                      body: { confirm: confirmText },
                    }),
                  );
                  setNote(`${res.filesBefore} file(s) removed. The project itself is untouched.`);
                  setClearing(false);
                  setConfirmText("");
                  onChanged(project);
                } catch (err) {
                  setError(err as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              Empty it
            </button>
          </>
        }
      >
        <ErrorBox error={error} />
        <p style={{ marginTop: 0 }}>
          Every file goes. The project, its address, its schedulers and its links stay.
          {project.gitTracked ? " The history keeps them — this is one commit." : " There is no history to undo it."}
        </p>
        <Field label={`Type ${project.title} to confirm`}>
          <input value={confirmText} onChange={(e) => setConfirmText(e.target.value)} autoFocus />
        </Field>
      </Modal>
    );
  }

  if (confirming) {
    return (
      <ConfirmDelete
        what="project"
        name={project.title}
        downloadUrl={`/api/projects/${project.id}/download`}
        onClose={() => setConfirming(false)}
        onConfirm={async () => {
          try {
            await guarded(`deleting the project ${project.title}`, () =>
              api(`/api/projects/${project.id}?confirm=${encodeURIComponent(project.title)}`, { method: "DELETE" }),
            );
            onDeleted?.();
            onClose();
          } catch (err) {
            setError(err as Error);
            setConfirming(false);
          }
        }}
        details={
          <ul style={{ margin: "0 0 12px", paddingLeft: 20 }}>
            <li>{preview?.files ?? 0} files</li>
            <li>
              the branch <code className="mono">{preview?.branch}</code>
              {preview?.hasHistory ? " with its history" : " (no commits yet)"}
            </li>
            <li>
              schedulers: {(preview?.schedulers ?? []).join(", ") || "none"}
            </li>
            <li>
              links pointing here: {(preview?.linksPointingHere ?? []).join(", ") || "none"}
              {(preview?.linksPointingHere?.length ?? 0) > 0 ? " — those links go, the originals stay" : ""}
            </li>
          </ul>
        }
      />
    );
  }

  return (
    <Modal
      title={`Settings · ${project.title}`}
      onClose={onClose}
      footer={
        <>
          <button
            className="btn danger"
            onClick={async () => {
              try {
                setPreview(await api(`/api/projects/${project.id}/deletion-preview`));
                setConfirming(true);
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            <Icon name="trash" size={15} /> Delete
          </button>
          <div style={{ flex: 1 }} />
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save} disabled={busy}>
            Save
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      {note ? <div className="warning">{note}</div> : null}

      <Section title="What it is" />

      <Field label="Name">
        <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} />
      </Field>

      <Field
        label="Address"
        hint={
          form.slug !== project.slug ? (
            <span style={{ color: "var(--ctp-peach)" }}>
              This renames the branch. Old clone addresses stop working.
            </span>
          ) : (
            "The address is the branch name in the group's repository."
          )
        }
      >
        <input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} />
      </Field>

      <Field label="Description">
        <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
      </Field>

      <Field label="Group" hint="Moving it carries the branch and its history over.">
        <select
          value={pendingMove?.groupId ?? project.groupId ?? ""}
          onChange={(e) => askAboutMove(e.target.value || "ungrouped")}
        >
          <option value="">Ungrouped</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.title}
            </option>
          ))}
        </select>
      </Field>

      {pendingMove ? (
        <div className="tile" style={{ marginBottom: 14 }}>
          <div className="list" style={{ background: "transparent", border: "none" }}>
            {pendingMove.notes.map((n, i) => (
              <div key={i} className="list-row" style={{ padding: "4px 0" }}>
                <span className={`badge ${n.level === "breaks" ? "bad" : n.level === "changes" ? "warn" : "good"}`}>
                  {n.level}
                </span>
                <span className="grow">{n.what}</span>
              </div>
            ))}
          </div>
          <div className="tile-foot">
            <div style={{ flex: 1 }} />
            <button className="btn small" onClick={() => setPendingMove(null)}>
              Cancel
            </button>
            <button
              className="btn small primary"
              disabled={pendingMove.notes.some((n) => n.level === "breaks")}
              onClick={() => move(pendingMove.groupId)}
            >
              Move it
            </button>
          </div>
        </div>
      ) : null}

      <Section title="Who may see it" />

      <div className="row">
        <Field label="Visibility">
          <select
            value={form.visibility}
            onChange={(e) => setForm({ ...form, visibility: e.target.value as Project["visibility"] })}
          >
            <option value="group">As the group</option>
            <option value="private">Private — only you</option>
            <option value="public">Public — anyone may look</option>
            <option value="password">Password — anyone who knows it</option>
          </select>
        </Field>
        {form.visibility === "password" ? (
          <Field label={project.hasPassword ? "New password (empty keeps it)" : "Password"}>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
        ) : null}
      </div>

      {form.visibility === "public" ? (
        <label className="check">
          <input
            type="checkbox"
            checked={form.anonWrite}
            onChange={(e) => setForm({ ...form, anonWrite: e.target.checked })}
          />
          <span>Visitors without an account may create and edit files here (rate-limited).</span>
        </label>
      ) : null}

      <Field label="Capabilities" hint="Views onto the same files.">
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          {(meta?.capabilities ?? []).map((c) => {
            const on = form.capabilities.includes(c.name);
            return (
              <button
                key={c.name}
                className={on ? "btn primary small" : "btn small"}
                onClick={() =>
                  setForm({
                    ...form,
                    capabilities: on
                      ? form.capabilities.filter((n) => n !== c.name)
                      : [...form.capabilities, c.name],
                  })
                }
                title={c.owns?.length ? `owns ${c.owns.join(", ")}` : c.title}
              >
                <Icon name={c.icon} size={14} /> {c.title}
              </button>
            );
          })}
        </div>
      </Field>

      <Field
        label="Publish as site"
        hint={
          <>
            The folder that gets served. Publishing does not make the project public — only that folder.
            {project.siteUrl ? (
              <>
                {" "}
                Live at <a href={project.siteUrl}>{project.siteUrl}</a>
              </>
            ) : null}
          </>
        }
      >
        <input
          value={form.siteRoot}
          list="site-candidates"
          placeholder="public"
          onChange={(e) => setForm({ ...form, siteRoot: e.target.value })}
        />
        <datalist id="site-candidates">
          {candidates.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
      </Field>

      <Field label="Colour">
        <div className="swatches">
          {(meta?.colors ?? []).map((name) => (
            <button
              key={name}
              className={form.color === name ? "swatch selected" : "swatch"}
              style={{ background: colorVar(name) }}
              title={name}
              onClick={() => setForm({ ...form, color: name })}
            />
          ))}
        </div>
      </Field>

      <Field label="Icon">
        <div className="swatches">
          {(meta?.icons ?? []).map((name) => (
            <button
              key={name}
              className="swatch"
              style={{
                background: "var(--ctp-surface0)",
                display: "grid",
                placeItems: "center",
                borderColor: form.icon === name ? "var(--ctp-text)" : "transparent",
              }}
              title={name}
              onClick={() => setForm({ ...form, icon: name })}
            >
              <Icon name={name} size={16} />
            </button>
          ))}
        </div>
      </Field>

      <label className="check">
        <input
          type="checkbox"
          checked={form.gitTracked}
          onChange={(e) => setForm({ ...form, gitTracked: e.target.checked })}
        />
        <span>
          Git tracking — commit automatically on every change. Off by default, so the history stays readable.
        </span>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.readOnly}
          onChange={(e) => setForm({ ...form, readOnly: e.target.checked })}
        />
        <span>
          Read-only — no uploads, no editing, <strong>no git push</strong>. Schedulers writing here are paused
          and named.
        </span>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.archived}
          onChange={(e) => setForm({ ...form, archived: e.target.checked })}
        />
        <span>Archive — out of the listings, still readable, data stays.</span>
      </label>

      <Section title="What comes in" />

      <Field
        label="Drop-off"
        hint="A link for handing material over without an account here. Nothing is stored: no account, no password."
      >
        <div className="builder">
          <select value={onewayKind} onChange={(e) => setOnewayKind(e.target.value)}>
            <option value="">— what they have —</option>
            {onewayKinds.map((k) => (
              <option key={k.name} value={k.name}>
                {k.title}
              </option>
            ))}
          </select>
          <button
            className="btn small"
            disabled={!onewayKind}
            onClick={async () => {
              try {
                const res = await api<{ url: string }>(`/api/projects/${project.id}/oneway/link`, {
                  body: { kind: onewayKind, days: 7 },
                });
                setOnewayLink(res.url);
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Make a link
          </button>
        </div>
        {onewayLink ? <Copyable value={onewayLink} /> : null}
      </Field>

      <Field label="Filters" hint="Rules that sort what lands here. Written once under Filters, picked up here.">
        <ProjectFilters project={project} />
      </Field>

      <Section title="Getting it out" />

      {project.cloneUrl ? (
        <Field label="Clone">
          <Copyable value={`git clone ${project.cloneUrl}`} />
        </Field>
      ) : null}

      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <a className="btn" href={`/api/projects/${project.id}/download`}>
          <Icon name="download" size={15} /> Download as ZIP
        </a>
        <button className="btn danger" onClick={() => setClearing(true)}>
          <Icon name="trash" size={15} /> Empty it
        </button>
        <button
          className="btn"
          onClick={async () => {
            try {
              const copy = await api<Project>(`/api/projects/${project.id}/duplicate`, { method: "POST", body: {} });
              onChanged(copy);
              onClose();
            } catch (err) {
              setError(err as Error);
            }
          }}
        >
          <Icon name="copy" size={15} /> Duplicate
        </button>
      </div>
    </Modal>
  );
}

interface MoveNote {
  level: "breaks" | "changes" | "fine";
  what: string;
}
