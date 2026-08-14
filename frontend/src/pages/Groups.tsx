import { useState } from "react";
import { Link } from "react-router-dom";
import GroupSettings from "../components/GroupSettings";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Menu, Modal, Spinner } from "../components/ui";
import { api, type Group, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

type Payload = { groups: Group[]; ungrouped: { id: string; slug: string; title: string; color: string; icon: string }[] };

export default function Groups() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<Payload>("/api/groups");
  const [creating, setCreating] = useState(false);
  const [settingsFor, setSettingsFor] = useState<Group | null>(null);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Groups</h1>
        </div>
        {session.user ? (
          <div className="head-actions">
            <button className="btn primary" onClick={() => setCreating(true)}>
              <Icon name="plus" size={16} /> New group
            </button>
          </div>
        ) : null}
      </div>

      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {data && data.groups.length === 0 && data.ungrouped.length === 0 ? (
        <Empty icon="folder">{session.user ? "No groups yet." : "Nothing public."}</Empty>
      ) : null}

      <div className="tiles">
        {data?.groups.map((g) => (
          <Link
            key={g.id}
            to={`/groups/${g.slug}`}
            className="tile"
            style={{ ["--tile-color" as string]: colorVar(g.color) }}
          >
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name={g.icon} />
              </span>
              <div style={{ minWidth: 0 }}>
                <h3>{g.title}</h3>
                <div className="sub">
                  {g.projectCount} project{g.projectCount === 1 ? "" : "s"}
                </div>
              </div>
            </div>
            {g.description ? <div className="sub">{g.description}</div> : null}
            <div className="tile-foot">
              {g.visibility !== "private" ? (
                <span className="badge">
                  <Icon name={g.visibility === "public" ? "eye" : "lock"} size={12} /> {g.visibility}
                </span>
              ) : null}
              {g.readOnly ? (
                <span className="badge warn">
                  <Icon name="lock" size={12} /> read-only
                </span>
              ) : null}
              {g.pinned ? <span className="badge">pinned</span> : null}
              {g.archived ? <span className="badge">archived</span> : null}
            </div>
            {session.user ? (
              <div className="tile-menu">
                <Menu
                  label={`Settings for ${g.title}`}
                  items={[
                    { label: "Open", icon: "chevronRight", onClick: () => (location.href = `/groups/${g.slug}`) },
                    { label: "Settings", icon: "settings", onClick: () => setSettingsFor(g) },
                    { label: "Download ZIP", icon: "download", onClick: () => (location.href = `/api/groups/${g.slug}/download`) },
                  ]}
                />
              </div>
            ) : null}
          </Link>
        ))}
      </div>

      {data && data.ungrouped.length > 0 ? (
        <>
          <h2 style={{ fontSize: 17, marginTop: 32 }}>Ungrouped</h2>
          <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
            Projects without a group. Not an area of its own — simply the ones whose group is empty.
          </p>
          <div className="tiles">
            {data.ungrouped.map((p) => (
              <Link
                key={p.id}
                to={`/p/${p.id}`}
                className="tile"
                style={{ ["--tile-color" as string]: colorVar(p.color) }}
              >
                <div className="tile-top">
                  <span className="tile-icon">
                    <Icon name={p.icon || "box"} />
                  </span>
                  <h3>{p.title}</h3>
                </div>
              </Link>
            ))}
          </div>
        </>
      ) : null}

      {creating ? <CreateGroup onClose={() => setCreating(false)} onCreated={reload} /> : null}
      {settingsFor ? (
        <GroupSettings
          group={settingsFor}
          onClose={() => setSettingsFor(null)}
          onChanged={() => {
            setSettingsFor(null);
            reload();
          }}
          onDeleted={reload}
        />
      ) : null}
    </>
  );
}

function CreateGroup({ onClose, onCreated }: { onClose: () => void; onCreated: (g: Group) => void }) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <Modal
      title="New group"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy || !title.trim()}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                const group = await api<Group>("/api/groups", { body: { title, description } });
                onCreated(group);
                onClose();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Create
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <p style={{ marginTop: 0, color: "var(--ctp-subtext0)" }}>
        A bare git repository is created with it, automatically. Every project in the group becomes a branch.
      </p>
      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus placeholder="Studies" />
      </Field>
      <Field label="Description">
        <input value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
    </Modal>
  );
}

export type { Project };
