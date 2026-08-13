import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/** Notes: an editor over the project's *.md files, with backlinks. */
export default function MarkdownView({ project }: { project: Project; reload: () => void }) {
  const [params, setParams] = useSearchParams();
  const path = params.get("note");
  const { data, error, loading, reload } = useQuery<{
    notes: { path: string; title: string; size: number; modifiedAt: string }[];
  }>(`/api/projects/${project.id}/markdown/notes`);
  const [creating, setCreating] = useState(false);

  const open = (next: string | null) => {
    const p = new URLSearchParams(params);
    if (next) p.set("note", next);
    else p.delete("note");
    setParams(p);
  };

  return (
    <div className="grid-2" style={{ gridTemplateColumns: "minmax(220px, 300px) 1fr", alignItems: "start" }}>
      <div>
        <div style={{ display: "flex", gap: 8, marginBottom: 10 }}>
          <strong style={{ flex: 1 }}>{data?.notes.length ?? 0} notes</strong>
          {!project.readOnly ? (
            <button className="btn small" onClick={() => setCreating(true)}>
              <Icon name="plus" size={14} /> New
            </button>
          ) : null}
        </div>
        <ErrorBox error={error} onRetry={reload} />
        {loading && !data ? <Spinner /> : null}
        {data && data.notes.length === 0 ? <Empty icon="notebook">No notes yet.</Empty> : null}
        <div className="list">
          {data?.notes.map((n) => (
            <button
              key={n.path}
              className="list-row"
              style={{
                background: n.path === path ? "var(--ctp-surface0)" : undefined,
                border: "none",
                cursor: "pointer",
                textAlign: "left",
                width: "100%",
              }}
              onClick={() => open(n.path)}
            >
              <Icon name="file" size={15} />
              <span className="grow">{n.path}</span>
            </button>
          ))}
        </div>
      </div>

      <div>
        {path ? (
          <NoteEditor project={project} path={path} onClose={() => open(null)} onSaved={reload} />
        ) : (
          <Empty icon="notebook">
            Pick a note. The vault on your machine stays in sync through this project's git branch.
          </Empty>
        )}
      </div>

      {creating ? (
        <NewNote
          project={project}
          onClose={() => setCreating(false)}
          onCreated={(p) => {
            setCreating(false);
            reload();
            open(p);
          }}
        />
      ) : null}
    </div>
  );
}

function NoteEditor({
  project,
  path,
  onClose,
  onSaved,
}: {
  project: Project;
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { data, error, loading } = useQuery<{ content: string; title: string }>(
    `/api/projects/${project.id}/markdown/note?path=${encodeURIComponent(path)}`,
  );
  const backlinks = useQuery<{ backlinks: string[] }>(
    `/api/projects/${project.id}/markdown/backlinks?path=${encodeURIComponent(path)}`,
  );
  const [content, setContent] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<Error | null>(null);
  const [saved, setSaved] = useState(false);
  const value = content ?? data?.content ?? "";

  return (
    <div>
      <div className="crumbs">
        <strong>{path}</strong>
        <div style={{ flex: 1 }} />
        <button className="btn small ghost" onClick={onClose}>
          <Icon name="x" size={14} />
        </button>
        {!project.readOnly ? (
          <button
            className="btn small primary"
            onClick={async () => {
              setSaveError(null);
              try {
                await api(`/api/projects/${project.id}/markdown/note`, {
                  method: "PUT",
                  body: { path, content: value },
                });
                setSaved(true);
                setTimeout(() => setSaved(false), 1200);
                onSaved();
                backlinks.reload();
              } catch (err) {
                setSaveError(err as Error);
              }
            }}
          >
            <Icon name="check" size={14} /> {saved ? "Saved" : "Save"}
          </button>
        ) : null}
      </div>
      <ErrorBox error={saveError ?? error} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <textarea
          className="editor"
          value={value}
          readOnly={project.readOnly}
          spellCheck={false}
          onChange={(e) => setContent(e.target.value)}
        />
      ) : null}

      <div style={{ marginTop: 14 }}>
        <h3 style={{ fontSize: 15 }}>Linked from</h3>
        {backlinks.data?.backlinks.length ? (
          <div className="list">
            {backlinks.data.backlinks.map((b) => (
              <div key={b} className="list-row">
                <Icon name="link" size={14} />
                <span className="grow">{b}</span>
              </div>
            ))}
          </div>
        ) : (
          <p style={{ color: "var(--ctp-subtext0)" }}>
            Nothing points here yet. Write <code className="mono">[[{data?.title ?? "note"}]]</code> in another
            note and it will.
          </p>
        )}
        {data ? (
          <p className="meta" style={{ color: "var(--ctp-overlay1)" }}>
            last change {formatDate(new Date().toISOString())}
          </p>
        ) : null}
      </div>
    </div>
  );
}

function NewNote({
  project,
  onClose,
  onCreated,
}: {
  project: Project;
  onClose: () => void;
  onCreated: (path: string) => void;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState<Error | null>(null);
  return (
    <Modal
      title="New note"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!name.trim()}
            onClick={async () => {
              const path = name.endsWith(".md") ? name : `${name}.md`;
              try {
                await api(`/api/projects/${project.id}/markdown/note`, {
                  method: "PUT",
                  body: { path, content: `# ${name.replace(/\.md$/, "")}\n\n` },
                });
                onCreated(path);
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Create
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Name" hint="Folders are allowed: semester-3/analysis.md">
        <input value={name} onChange={(e) => setName(e.target.value)} autoFocus placeholder="ideas.md" />
      </Field>
    </Modal>
  );
}
