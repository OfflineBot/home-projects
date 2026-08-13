import { useRef, useState } from "react";
import { api, type Project } from "../lib/api";
import { useMeta } from "../lib/store";
import { Icon } from "./Icon";
import { ErrorBox, Field, Modal, formatBytes } from "./ui";

/**
 * Creating a project: pick a preset, not a list of switches. The preset is a
 * starting point, not a cage — every capability can be turned on or off
 * afterwards, and the preset only ever decided the icon and the first tab.
 *
 * A project can also start from a zip: upload an archive and its contents
 * become the file tree.
 */
export default function CreateProject({
  groupId,
  groupTitle,
  onClose,
  onCreated,
}: {
  groupId?: string;
  groupTitle?: string;
  onClose: () => void;
  onCreated: (p: Project) => void;
}) {
  const meta = useMeta();
  const [preset, setPreset] = useState("data");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [zip, setZip] = useState<File | null>(null);
  const [clearFirst, setClearFirst] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      setProgress("creating the project…");
      const project = await api<Project>("/api/projects", {
        body: { title, description, groupId: groupId ?? "", preset },
      });

      if (zip) {
        setProgress(`unpacking ${zip.name}…`);
        const form = new FormData();
        form.append("file", zip);
        form.append("clear", String(clearFirst));
        const result = await api<{ files: number; skipped: string[] }>(
          `/api/projects/${project.id}/files/import-zip`,
          { method: "POST", raw: form },
        );
        if (result.skipped?.length) {
          setProgress(`${result.files} files taken over, ${result.skipped.length} skipped`);
        }
      }

      onCreated(project);
      onClose();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
      setProgress(null);
    }
  };

  return (
    <Modal
      title={groupTitle ? `New project in ${groupTitle}` : "New project"}
      onClose={onClose}
      footer={
        <>
          {progress ? <span style={{ color: "var(--ctp-subtext0)" }}>{progress}</span> : null}
          <div style={{ flex: 1 }} />
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={create} disabled={busy || !title.trim()}>
            Create
          </button>
        </>
      }
    >
      <ErrorBox error={error} />

      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus placeholder="Timetable" />
      </Field>
      <Field label="Description">
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this is for"
        />
      </Field>

      <Field label="What it starts as" hint="Only the icon and the first tab. Everything else stays open.">
        <div className="tiles" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))" }}>
          {(meta?.presets ?? []).map((p) => (
            <button
              key={p.key}
              className="tile"
              style={{
                textAlign: "left",
                cursor: "pointer",
                padding: 12,
                borderColor: preset === p.key ? "var(--accent)" : undefined,
              }}
              onClick={() => setPreset(p.key)}
            >
              <div className="tile-top">
                <span className="tile-icon" style={{ width: 28, height: 28 }}>
                  <Icon name={p.icon} size={15} />
                </span>
                <h3 style={{ fontSize: 14 }}>{p.title}</h3>
              </div>
              <div className="sub" style={{ fontSize: 12 }}>
                {p.description}
              </div>
            </button>
          ))}
        </div>
      </Field>

      <Field
        label="Start from a zip (optional)"
        hint="The archive's contents become the project's files. A single wrapping folder is dropped."
      >
        <div
          className={zip ? "dropzone over" : "dropzone"}
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault();
            const file = e.dataTransfer.files[0];
            if (file) setZip(file);
          }}
          onClick={() => fileInput.current?.click()}
          style={{ cursor: "pointer" }}
        >
          <input
            ref={fileInput}
            type="file"
            accept=".zip,application/zip"
            style={{ display: "none" }}
            onChange={(e) => setZip(e.target.files?.[0] ?? null)}
          />
          {zip ? (
            <div>
              <Icon name="file" /> <strong>{zip.name}</strong> · {formatBytes(zip.size)}
              <div>
                <button
                  className="btn small"
                  style={{ marginTop: 8 }}
                  onClick={(e) => {
                    e.stopPropagation();
                    setZip(null);
                  }}
                >
                  Remove
                </button>
              </div>
            </div>
          ) : (
            <>
              <Icon name="upload" size={22} />
              <div>Drop a .zip here, or click to pick one</div>
            </>
          )}
        </div>
      </Field>
      {zip ? (
        <label className="check">
          <input type="checkbox" checked={clearFirst} onChange={(e) => setClearFirst(e.target.checked)} />
          <span>Replace the preset's starting files with the archive's contents</span>
        </label>
      ) : null}
    </Modal>
  );
}
