import { useState } from "react";
import { api, authedUrl, type Group, type Project } from "../lib/api";
import { useMeta } from "../lib/store";
import { colorVar } from "../lib/theme";
import { Icon, iconNames } from "./Icon";
import { ConfirmDelete, Copyable, ErrorBox, Field, Modal, Section, useGuarded } from "./ui";

/**
 * Everything the settings table in section 7 lists for a group. It opens both
 * from the group's own page and from the menu on its tile.
 */
export default function GroupSettings({
  group,
  projects,
  onClose,
  onChanged,
  onDeleted,
}: {
  group: Group;
  projects?: Project[];
  onClose: () => void;
  onChanged: (group: Group) => void;
  onDeleted?: () => void;
}) {
  const meta = useMeta();
  const guarded = useGuarded();
  const [form, setForm] = useState({
    title: group.title,
    slug: group.slug,
    description: group.description,
    visibility: group.visibility,
    color: group.color,
    icon: group.icon,
    pinned: group.pinned,
    readOnly: group.readOnly,
    pushWithPassword: group.pushWithPassword,
    gitVisibility: group.gitVisibility ?? "",
    archived: group.archived,
    siteProjectId: group.siteProjectId ?? "",
  });
  const [password, setPassword] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [preview, setPreview] = useState<any>(null);
  const [withProjects, setWithProjects] = useState(false);

  const slugChanged = form.slug !== group.slug;

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        title: form.title,
        description: form.description,
        color: form.color,
        icon: form.icon,
        pinned: form.pinned,
        readOnly: form.readOnly,
        pushWithPassword: form.pushWithPassword,
        gitVisibility: form.gitVisibility,
        archived: form.archived,
        siteProjectId: form.siteProjectId,
      };
      if (form.slug !== group.slug) body.slug = form.slug;
      if (form.visibility !== group.visibility) body.visibility = form.visibility;
      if (password) body.password = password;

      const updated = await guarded("changing who can see this group", () =>
        api<Group>(`/api/groups/${group.slug}`, { method: "PATCH", body }),
      );
      onChanged(updated);
      onClose();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  const openDelete = async () => {
    setError(null);
    try {
      setPreview(await api(`/api/groups/${group.slug}/deletion-preview`));
      setConfirming(true);
    } catch (err) {
      setError(err as Error);
    }
  };

  if (confirming) {
    return (
      <ConfirmDelete
        what="group"
        name={group.title}
        downloadUrl={`/api/groups/${group.slug}/download`}
        onClose={() => setConfirming(false)}
        onConfirm={async () => {
          try {
            await guarded(`deleting the group ${group.title}`, () =>
              api(`/api/groups/${group.slug}?confirm=${encodeURIComponent(group.title)}&withProjects=${withProjects}`, {
                method: "DELETE",
              }),
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
            <li>
              the repository <code className="mono">{preview?.repository}</code> with{" "}
              {preview?.branches?.length ?? 0} branch(es)
            </li>
            <li>
              {preview?.projects?.length ?? 0} project(s): {(preview?.projects ?? []).join(", ") || "none"}
            </li>
            <li>{preview?.files ?? 0} files</li>
          </ul>
        }
        extra={
          (preview?.projects?.length ?? 0) > 0 ? (
            <label className="check">
              <input type="checkbox" checked={withProjects} onChange={(e) => setWithProjects(e.target.checked)} />
              <span>
                Delete the projects too. Left off, they move to <strong>Ungrouped</strong> and keep their history.
              </span>
            </label>
          ) : null
        }
      />
    );
  }

  return (
    <Modal
      title={`Settings · ${group.title}`}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn danger" onClick={openDelete}>
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

      <Section title="What it is" />

      <Field label="Name">
        <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} />
      </Field>

      <Field
        label="Address"
        hint={
          slugChanged ? (
            <span style={{ color: "var(--ctp-peach)" }}>
              This renames the repository. Old clone addresses stop working.
            </span>
          ) : (
            <>The address is the repository name and the URL segment.</>
          )
        }
      >
        <input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} />
      </Field>

      <Field label="Description">
        <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
      </Field>

      <div className="row">
      <Section title="Who may see it" />

        <Field label="Visibility">
          <select
            value={form.visibility}
            onChange={(e) => setForm({ ...form, visibility: e.target.value as Group["visibility"] })}
          >
            <option value="private">Private — only you</option>
            <option value="public">Public — anyone may look and clone</option>
            <option value="password">Password — anyone who knows it</option>
          </select>
        </Field>

      <Section title="Repository" />

        <Field
          label="Clone"
          hint={
            form.gitVisibility === ""
              ? "As open as the group."
              : "Answered on its own, whatever the group says."
          }
        >
          <select
            value={form.gitVisibility}
            onChange={(e) => setForm({ ...form, gitVisibility: e.target.value })}
          >
            <option value="">as the group</option>
            <option value="public">public — anyone may clone</option>
            <option value="password">password — the group's password</option>
            <option value="private">private — only signed in</option>
          </select>
        </Field>
        {form.visibility === "password" ? (
          <Field label={group.hasPassword ? "New password (leave empty to keep)" : "Password"}>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
        ) : null}
      </div>

      <Section title="Take it elsewhere" />

      <p className="hint" style={{ marginTop: 0 }}>
        A bundle is this group as one file: its projects and their files, the filters it uses, the
        schedulers, and the accounts they point at — without the passwords. On the other server,
        import it and type the passwords in once.
      </p>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 6 }}>
        {([
          { label: "Everything", query: "" },
          { label: "Without the schedulers", query: "&schedulers=false" },
          { label: "Without the files", query: "&files=false" },
          { label: "Shape only", query: "&files=false&schedulers=false&accounts=false&filters=false" },
        ] as const).map((choice) => (
          <a
            key={choice.label}
            className="btn small"
            href={authedUrl(`/api/export/bundle?group=${group.slug}${choice.query}`)}
          >
            <Icon name="archive" size={14} /> {choice.label}
          </a>
        ))}
      </div>

      <Field label="Colour" >
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
          {(meta?.icons ?? iconNames.slice(0, 20)).map((name) => (
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

      {projects && projects.length > 0 ? (
        <Field
          label="Site at the group's address"
          hint="One project of this group can run at /s/<group>/ directly."
        >
          <select
            value={form.siteProjectId}
            onChange={(e) => setForm({ ...form, siteProjectId: e.target.value })}
          >
            <option value="">— none —</option>
            {projects
              .filter((p) => p.siteRoot)
              .map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                </option>
              ))}
          </select>
        </Field>
      ) : null}

      {form.visibility === "password" ? (
        <label className="check">
          <input
            type="checkbox"
            checked={form.pushWithPassword}
            onChange={(e) => setForm({ ...form, pushWithPassword: e.target.checked })}
          />
          <span>
            <strong>The password may push, too.</strong> Then{" "}
            <code className="mono">git push</code> needs no account — the repository's password is enough,
            in the basic-auth field. Read-only projects still refuse it, and branches the password may not
            see are not offered. Off by default: a password that may read is a different thing from one
            that may write.
          </span>
        </label>
      ) : null}
      <label className="check">
        <input type="checkbox" checked={form.pinned} onChange={(e) => setForm({ ...form, pinned: e.target.checked })} />
        <span>Pin to the dashboard</span>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.readOnly}
          onChange={(e) => setForm({ ...form, readOnly: e.target.checked })}
        />
        <span>
          Read-only — freezes the group and everything in it: no uploads, no editing, <strong>no git push</strong>.
          Schedulers that would write into it are paused and named.
        </span>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.archived}
          onChange={(e) => setForm({ ...form, archived: e.target.checked })}
        />
        <span>Archive — disappears from the listings, stays readable, data stays.</span>
      </label>

      {group.cloneUrl ? (
        <Field label="Clone">
          <Copyable value={`git clone ${group.cloneUrl}`} />
        </Field>
      ) : null}

      <a className="btn" href={`/api/groups/${group.slug}/download`}>
        <Icon name="download" size={15} /> Download as ZIP
      </a>
    </Modal>
  );
}
