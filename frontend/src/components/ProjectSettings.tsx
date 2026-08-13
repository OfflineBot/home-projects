import { useEffect, useState } from "react";
import { api, type Group, type Project } from "../lib/api";
import { useMeta } from "../lib/store";
import { colorVar } from "../lib/theme";
import { Icon } from "./Icon";
import { ConfirmDelete, Copyable, ErrorBox, Field, Modal, useGuarded } from "./ui";

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
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [preview, setPreview] = useState<any>(null);
  const [note, setNote] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<string[]>([]);

  useEffect(() => {
    void api<{ groups: Group[] }>("/api/groups")
      .then((r) => setGroups(r.groups))
      .catch(() => undefined);
    if (project.capabilities.includes("site")) {
      void api<{ candidates: string[] }>(`/api/projects/${project.id}/site/candidates`)
        .then((r) => setCandidates(r.candidates))
        .catch(() => undefined);
    }
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

  const move = async (groupId: string) => {
    setError(null);
    try {
      const result = await api<{ project: Project }>(`/api/projects/${project.id}/move`, {
        method: "POST",
        body: { groupId },
      });
      onChanged(result.project);
    } catch (err) {
      setError(err as Error);
    }
  };

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

      <Field label="Group" hint="Moving it carries the history over as a branch move.">
        <select value={project.groupId ?? ""} onChange={(e) => move(e.target.value || "ungrouped")}>
          <option value="">Ungrouped</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.title}
            </option>
          ))}
        </select>
      </Field>

      <div className="row">
        <Field label="Visibility">
          <select
            value={form.visibility}
            onChange={(e) => setForm({ ...form, visibility: e.target.value as Project["visibility"] })}
          >
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

      <Field label="Capabilities" hint="Views onto the same files. Every combination is allowed.">
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
                title={`owns ${c.owns.join(", ")}`}
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

      {project.cloneUrl ? (
        <Field label="Clone">
          <Copyable value={`git clone ${project.cloneUrl}`} />
        </Field>
      ) : null}

      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <a className="btn" href={`/api/projects/${project.id}/download`}>
          <Icon name="download" size={15} /> Download as ZIP
        </a>
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
