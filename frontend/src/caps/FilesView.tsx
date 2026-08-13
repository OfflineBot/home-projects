import { useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Menu, Modal, Spinner, formatBytes, formatDate } from "../components/ui";
import { api, type FileEntry, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/**
 * The file tree — the foundation every project has, whatever else is switched
 * on. The path lives in the URL, so the back button works.
 */
export default function FilesView({ project, reload }: { project: Project; reload: () => void }) {
  const [params, setParams] = useSearchParams();
  const path = params.get("path") ?? "";
  const editing = params.get("file");

  const { data, error, loading, reload: reloadList } = useQuery<{
    path: string;
    entries: FileEntry[];
    readOnly: boolean;
    parent: string;
  }>(`/api/projects/${project.id}/files?path=${encodeURIComponent(path)}`);

  const [newFolder, setNewFolder] = useState(false);
  const [newFile, setNewFile] = useState(false);
  const [importing, setImporting] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [actionError, setActionError] = useState<Error | null>(null);
  const upload = useRef<HTMLInputElement>(null);
  const uploadFolder = useRef<HTMLInputElement>(null);

  const go = (next: string) => {
    const p = new URLSearchParams(params);
    if (next) p.set("path", next);
    else p.delete("path");
    p.delete("file");
    setParams(p);
  };

  const open = (entry: FileEntry) => {
    if (entry.isDir) return go(entry.path);
    const p = new URLSearchParams(params);
    p.set("file", entry.path);
    setParams(p);
  };

  const send = async (files: File[], relative?: string[]) => {
    setActionError(null);
    const form = new FormData();
    form.append("path", path);
    files.forEach((f, i) => {
      form.append("files", f);
      form.append("paths", relative?.[i] ?? f.name);
    });
    try {
      await api(`/api/projects/${project.id}/files/upload`, { method: "POST", raw: form });
      reloadList();
    } catch (err) {
      setActionError(err as Error);
    }
  };

  const remove = async (entry: FileEntry) => {
    setActionError(null);
    try {
      await api(
        `/api/projects/${project.id}/files?path=${encodeURIComponent(entry.path)}&recursive=${entry.isDir}`,
        { method: "DELETE" },
      );
      reloadList();
    } catch (err) {
      setActionError(err as Error);
    }
  };

  const rename = async (entry: FileEntry) => {
    const name = prompt("New name", entry.name);
    if (!name || name === entry.name) return;
    const parent = entry.path.slice(0, entry.path.length - entry.name.length);
    setActionError(null);
    try {
      await api(`/api/projects/${project.id}/files/move`, {
        method: "POST",
        body: { from: entry.path, to: parent + name },
      });
      reloadList();
    } catch (err) {
      setActionError(err as Error);
    }
  };

  if (editing) {
    return <FileEditor project={project} path={editing} readOnly={data?.readOnly ?? false} onClose={() => go(path)} />;
  }

  const crumbs = path ? path.split("/") : [];

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        const files = Array.from(e.dataTransfer.files);
        if (files.length) void send(files);
      }}
    >
      <div className="crumbs">
        <button onClick={() => go("")}>{project.title}</button>
        {crumbs.map((part, i) => (
          <span key={i} style={{ display: "flex", alignItems: "center", gap: 4 }}>
            <Icon name="chevronRight" size={13} />
            <button onClick={() => go(crumbs.slice(0, i + 1).join("/"))}>{part}</button>
          </span>
        ))}
        <div style={{ flex: 1 }} />
        {!data?.readOnly ? (
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
            <button className="btn small" onClick={() => setNewFile(true)}>
              <Icon name="file" size={14} /> New file
            </button>
            <button className="btn small" onClick={() => setNewFolder(true)}>
              <Icon name="folder" size={14} /> New folder
            </button>
            <button className="btn small" onClick={() => upload.current?.click()}>
              <Icon name="upload" size={14} /> Upload
            </button>
            <button className="btn small" onClick={() => uploadFolder.current?.click()}>
              <Icon name="upload" size={14} /> Folder
            </button>
            <button className="btn small" onClick={() => setImporting(true)}>
              <Icon name="box" size={14} /> From ZIP
            </button>
          </div>
        ) : null}
      </div>

      <input
        ref={upload}
        type="file"
        multiple
        style={{ display: "none" }}
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          if (files.length) void send(files);
          e.target.value = "";
        }}
      />
      <input
        ref={uploadFolder}
        type="file"
        multiple
        // @ts-expect-error — non-standard but supported everywhere that matters
        webkitdirectory=""
        directory=""
        style={{ display: "none" }}
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          const rel = files.map((f) => (f as any).webkitRelativePath || f.name);
          if (files.length) void send(files, rel);
          e.target.value = "";
        }}
      />

      <ErrorBox error={actionError ?? error} onRetry={reloadList} />
      {dragging ? <div className="dropzone over">Drop the files to upload them here</div> : null}
      {loading && !data ? <Spinner /> : null}

      {data && data.entries.length === 0 ? (
        <Empty icon="folder">This folder is empty.</Empty>
      ) : (
        <div className="list">
          {data?.entries.map((entry) => (
            <div key={entry.path} className="list-row" style={{ cursor: "pointer" }} onClick={() => open(entry)}>
              <Icon name={entry.isDir ? "folder" : "file"} size={16} />
              <span className="grow">
                {entry.name}
                {entry.linkId ? (
                  <span className="badge" style={{ marginLeft: 8 }}>
                    <Icon name="link" size={11} /> {entry.linkedFrom}
                  </span>
                ) : null}
              </span>
              <span className="meta">{entry.isDir ? "" : formatBytes(entry.size)}</span>
              <span className="meta">{formatDate(entry.modifiedAt)}</span>
              {!data.readOnly ? (
                <Menu
                  label={`Actions for ${entry.name}`}
                  items={[
                    {
                      label: "Download",
                      icon: "download",
                      onClick: () =>
                        (location.href = `/api/projects/${project.id}/files/download?path=${encodeURIComponent(entry.path)}`),
                    },
                    { label: "Rename", icon: "wrench", onClick: () => void rename(entry) },
                    "separator",
                    {
                      label: entry.linkId ? "Remove link (the original stays)" : "Delete",
                      icon: "trash",
                      danger: true,
                      onClick: () => void remove(entry),
                    },
                  ]}
                />
              ) : (
                <a
                  className="btn ghost icon"
                  href={`/api/projects/${project.id}/files/download?path=${encodeURIComponent(entry.path)}`}
                  onClick={(e) => e.stopPropagation()}
                >
                  <Icon name="download" size={15} />
                </a>
              )}
            </div>
          ))}
        </div>
      )}

      {newFolder ? (
        <NameDialog
          title="New folder"
          label="Name"
          onClose={() => setNewFolder(false)}
          onSubmit={async (name) => {
            await api(`/api/projects/${project.id}/files/folder`, { method: "POST", body: { path, name } });
            reloadList();
          }}
        />
      ) : null}

      {newFile ? (
        <NameDialog
          title="New file"
          label="Name, with its extension"
          placeholder="notes.md"
          onClose={() => setNewFile(false)}
          onSubmit={async (name) => {
            await api(`/api/projects/${project.id}/files/content`, {
              method: "PUT",
              body: { path: path ? `${path}/${name}` : name, content: "" },
            });
            reloadList();
          }}
        />
      ) : null}

      {importing ? (
        <ImportZip
          project={project}
          path={path}
          onClose={() => setImporting(false)}
          onDone={() => {
            setImporting(false);
            reloadList();
            reload();
          }}
        />
      ) : null}
    </div>
  );
}

function NameDialog({
  title,
  label,
  placeholder,
  onClose,
  onSubmit,
}: {
  title: string;
  label: string;
  placeholder?: string;
  onClose: () => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy || !name.trim()}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await onSubmit(name.trim());
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
      <Field label={label}>
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder={placeholder} autoFocus />
      </Field>
    </Modal>
  );
}

/** A project can also be filled from a zip — the counterpart of its download. */
function ImportZip({
  project,
  path,
  onClose,
  onDone,
}: {
  project: Project;
  path: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [clear, setClear] = useState(false);
  const [strip, setStrip] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ files: number; skipped: string[] } | null>(null);
  const input = useRef<HTMLInputElement>(null);

  return (
    <Modal
      title="Fill from a zip"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {result ? "Close" : "Cancel"}
          </button>
          <button
            className="btn primary"
            disabled={busy || !file}
            onClick={async () => {
              if (!file) return;
              setBusy(true);
              setError(null);
              try {
                const form = new FormData();
                form.append("file", file);
                form.append("path", path);
                form.append("clear", String(clear));
                form.append("strip", String(strip));
                const res = await api<{ files: number; skipped: string[] }>(
                  `/api/projects/${project.id}/files/import-zip`,
                  { method: "POST", raw: form },
                );
                setResult(res);
                onDone();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Unpack
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      {result ? (
        <div className="notice">
          {result.files} file(s) taken over.
          {result.skipped.length ? ` Skipped: ${result.skipped.slice(0, 10).join(", ")}` : ""}
        </div>
      ) : null}
      <div className="dropzone" onClick={() => input.current?.click()} style={{ cursor: "pointer" }}>
        <input
          ref={input}
          type="file"
          accept=".zip,application/zip"
          style={{ display: "none" }}
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
        {file ? (
          <>
            <Icon name="file" /> {file.name} · {formatBytes(file.size)}
          </>
        ) : (
          <>
            <Icon name="upload" size={22} />
            <div>Pick a .zip</div>
          </>
        )}
      </div>
      <label className="check" style={{ marginTop: 14 }}>
        <input type="checkbox" checked={strip} onChange={(e) => setStrip(e.target.checked)} />
        <span>Drop a single wrapping folder</span>
      </label>
      <label className="check">
        <input type="checkbox" checked={clear} onChange={(e) => setClear(e.target.checked)} />
        <span>Empty the project first</span>
      </label>
    </Modal>
  );
}

function FileEditor({
  project,
  path,
  readOnly,
  onClose,
}: {
  project: Project;
  path: string;
  readOnly: boolean;
  onClose: () => void;
}) {
  const { data, error, loading } = useQuery<{ content: string; linked: boolean }>(
    `/api/projects/${project.id}/files/content?path=${encodeURIComponent(path)}`,
  );
  const [content, setContent] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<Error | null>(null);
  const [saved, setSaved] = useState(false);
  const value = content ?? data?.content ?? "";

  return (
    <div>
      <div className="crumbs">
        <button onClick={onClose}>
          <Icon name="chevronLeft" size={13} /> back
        </button>
        <strong>{path}</strong>
        {data?.linked ? (
          <span className="badge">
            <Icon name="link" size={11} /> linked — the edit lands in the source
          </span>
        ) : null}
        <div style={{ flex: 1 }} />
        <a className="btn small" href={`/api/projects/${project.id}/files/download?path=${encodeURIComponent(path)}`}>
          <Icon name="download" size={14} /> Download
        </a>
        {!readOnly ? (
          <button
            className="btn small primary"
            onClick={async () => {
              setSaveError(null);
              try {
                await api(`/api/projects/${project.id}/files/content`, {
                  method: "PUT",
                  body: { path, content: value },
                });
                setSaved(true);
                setTimeout(() => setSaved(false), 1500);
              } catch (err) {
                setSaveError(err as Error);
              }
            }}
          >
            <Icon name={saved ? "check" : "check"} size={14} /> {saved ? "Saved" : "Save"}
          </button>
        ) : null}
      </div>
      <ErrorBox error={saveError ?? error} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <textarea
          className="editor"
          value={value}
          readOnly={readOnly}
          spellCheck={false}
          onChange={(e) => setContent(e.target.value)}
        />
      ) : null}
    </div>
  );
}
