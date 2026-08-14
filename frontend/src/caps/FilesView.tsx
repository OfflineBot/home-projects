import { useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Menu, Modal, Spinner, formatBytes, formatDate } from "../components/ui";
import { api, authedUrl, type FileEntry, type Project } from "../lib/api";
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
  const [sending, setSending] = useState<FileEntry | null>(null);
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
          {path ? (
            <div className="list-row" style={{ cursor: "pointer" }} onClick={() => go(data?.parent ?? "")}>
              <Icon name="chevronLeft" size={16} />
              <span className="grow meta">up one level</span>
            </div>
          ) : null}
          {data?.entries.map((entry) => (
            <div key={entry.path} className="list-row" style={{ cursor: "pointer" }} onClick={() => open(entry)}>
              <Icon name={iconFor(entry)} size={16} />
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
                        (location.href = authedUrl(
                          `/api/projects/${project.id}/files/download?path=${encodeURIComponent(entry.path)}`,
                        )),
                    },
                    { label: "Rename", icon: "wrench", onClick: () => void rename(entry) },
                    { label: "Send to another project…", icon: "link", onClick: () => setSending(entry) },
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
                  href={authedUrl(
                    `/api/projects/${project.id}/files/download?path=${encodeURIComponent(entry.path)}`,
                  )}
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

      {sending ? (
        <SendElsewhere
          project={project}
          entry={sending}
          onClose={() => setSending(null)}
          onDone={() => {
            setSending(null);
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

/**
 * One project pulls material in, another is where it gets arranged. This is the
 * step between them, in the three ways that are meaningfully different: a link
 * keeps pointing at what the scheduler refreshes, a copy freezes it as it is,
 * and a move takes it out of the first project.
 */
function SendElsewhere({
  project,
  entry,
  onClose,
  onDone,
}: {
  project: Project;
  entry: FileEntry;
  onClose: () => void;
  onDone: () => void;
}) {
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const [target, setTarget] = useState("");
  const [targetPath, setTargetPath] = useState(entry.name);
  const [mode, setMode] = useState<"link" | "copy" | "move">("link");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  const others = (projects.data?.projects ?? []).filter((p) => p.id !== project.id && !p.readOnly);

  return (
    <Modal
      title={`Send ${entry.name} to another project`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy || !target}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/projects/${project.id}/files/send`, {
                  body: { path: entry.path, targetProject: target, targetPath, mode },
                });
                onDone();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            {mode === "link" ? "Link it" : mode === "copy" ? "Copy it" : "Move it"}
          </button>
        </>
      }
    >
      <ErrorBox error={error} />

      <Field label="Into which project">
        <select value={target} onChange={(e) => setTarget(e.target.value)}>
          <option value="">— pick one —</option>
          {others.map((p) => (
            <option key={p.id} value={p.id}>
              {p.groupSlug ? `${p.groupSlug} / ` : ""}
              {p.title}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Under which name" hint="Folders are allowed: semester-1/analysis">
        <input value={targetPath} onChange={(e) => setTargetPath(e.target.value)} />
      </Field>

      <Field label="How">
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <label className="check" style={{ margin: 0 }}>
            <input type="radio" checked={mode === "link"} onChange={() => setMode("link")} />
            <span>
              <strong>Link</strong> — a second name for the same thing. No copy: edits act on the original,
              and what a scheduler refreshes stays fresh here. Removing the link never deletes anything.
            </span>
          </label>
          <label className="check" style={{ margin: 0 }}>
            <input type="radio" checked={mode === "copy"} onChange={() => setMode("copy")} />
            <span>
              <strong>Copy</strong> — a second, independent thing. From now on the two drift apart, and the
              next scheduler run does not touch this one.
            </span>
          </label>
          <label className="check" style={{ margin: 0 }}>
            <input type="radio" checked={mode === "move"} onChange={() => setMode("move")} />
            <span>
              <strong>Move</strong> — it leaves this project. Careful with anything a scheduler wrote: it
              comes back here on the next run.
            </span>
          </label>
        </div>
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

/**
 * What a file is decides how it is opened. A lecture slide is a PDF, and a
 * project full of them is unusable if the only answer to a click is "this file
 * is not text". Text goes into the editor; everything the browser can show is
 * shown; the rest says what it is and offers itself for download.
 */
type Shape = "text" | "pdf" | "image" | "audio" | "video" | "opaque";

const SHAPES: { shape: Shape; extensions: string[] }[] = [
  { shape: "pdf", extensions: ["pdf"] },
  { shape: "image", extensions: ["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico"] },
  { shape: "audio", extensions: ["mp3", "m4a", "wav", "ogg", "opus", "flac"] },
  { shape: "video", extensions: ["mp4", "webm", "mov", "mkv"] },
  {
    shape: "text",
    extensions: [
      "md", "txt", "ics", "json", "yaml", "yml", "toml", "csv", "tsv", "log", "sql",
      "go", "ts", "tsx", "js", "jsx", "css", "html", "xml", "sh", "py", "rs", "java",
      "conf", "ini", "env", "gitignore", "dockerfile", "makefile",
    ],
  },
];

function shapeOf(path: string): Shape {
  const name = path.split("/").pop() ?? "";
  const ext = name.includes(".") ? name.split(".").pop()!.toLowerCase() : name.toLowerCase();
  for (const s of SHAPES) if (s.extensions.includes(ext)) return s.shape;
  return "opaque";
}

/** What a thing looks like in the list. A folder full of PDFs should not read
 * as a wall of identical grey sheets. */
function iconFor(entry: FileEntry): string {
  if (entry.isDir) return "folder";
  const name = entry.name.toLowerCase();
  if (name.endsWith(".zip") || name.endsWith(".tar") || name.endsWith(".gz")) return "box";
  if (name.endsWith(".ics")) return "calendar";
  if (name.endsWith(".eml")) return "mail";
  if (name.endsWith(".md") || name.endsWith(".txt")) return "notebook";
  switch (shapeOf(entry.name)) {
    case "image":
      return "camera";
    case "audio":
      return "music";
    case "video":
      return "play";
    case "text":
      return "code";
    default:
      return "file";
  }
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
  const shape = shapeOf(path);
  const raw = authedUrl(`/api/projects/${project.id}/files/raw?path=${encodeURIComponent(path)}`);
  const download = authedUrl(`/api/projects/${project.id}/files/download?path=${encodeURIComponent(path)}`);

  const head = (extra?: React.ReactNode) => (
    <div className="crumbs">
      <button onClick={onClose}>
        <Icon name="chevronLeft" size={13} /> back
      </button>
      <strong>{path}</strong>
      <div style={{ flex: 1 }} />
      <a className="btn small" href={download}>
        <Icon name="download" size={14} /> Download
      </a>
      {extra}
    </div>
  );

  if (shape !== "text") {
    return (
      <div>
        {head(
          shape === "pdf" || shape === "image" ? (
            <a className="btn small" href={raw} target="_blank" rel="noreferrer">
              <Icon name="eye" size={14} /> Open in a tab
            </a>
          ) : null,
        )}
        {shape === "pdf" ? (
          <iframe className="file-preview" src={raw} title={path} />
        ) : shape === "image" ? (
          <img className="file-preview image" src={raw} alt={path} />
        ) : shape === "audio" ? (
          <audio controls src={raw} style={{ width: "100%" }} />
        ) : shape === "video" ? (
          <video className="file-preview" controls src={raw} />
        ) : (
          <Empty icon="file">
            This kind of file cannot be shown here. Download it — the copy on the server stays as it is.
          </Empty>
        )}
      </div>
    );
  }

  return <TextFile project={project} path={path} readOnly={readOnly} onClose={onClose} download={download} />;
}

function TextFile({
  project,
  path,
  readOnly,
  onClose,
  download,
}: {
  project: Project;
  path: string;
  readOnly: boolean;
  onClose: () => void;
  download: string;
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
        <a className="btn small" href={download}>
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
