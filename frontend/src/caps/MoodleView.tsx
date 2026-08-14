import { useState } from "react";
import { Icon } from "../components/Icon";
import { ErrorBox, Field } from "../components/ui";
import { api, type Project } from "../lib/api";

/**
 * One pull, run by hand, with the password typed on the spot.
 *
 * Nothing here is stored — no account, no scheduler, no password. That is the
 * whole point: there is no credential that could be used up, so a typo costs
 * nothing but a second attempt. Anything that should happen on a schedule
 * wants an account and a scheduler instead.
 */
export default function MoodleView({ project }: { project: Project; reload: () => void }) {
  const [form, setForm] = useState({
    url: "",
    user: "",
    password: "",
    target: "",
    courses: "",
    routes: "",
    onlyCurrent: true,
    flat: false,
    prune: false,
    rebuild: false,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [result, setResult] = useState<{ message: string; files: number; log: string[] } | null>(null);

  const pull = async () => {
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const res = await api<{ message: string; files: number; log: string[] }>(
        `/api/projects/${project.id}/moodle/pull-once`,
        { body: form },
      );
      setResult(res);
      // The password has done its job and has no reason to stay in a form field.
      setForm({ ...form, password: "" });
    } catch (err) {
      setError(err as Error);
      setForm({ ...form, password: "" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ maxWidth: 720 }}>
      <div className="notice">
        <strong>Nothing is stored</strong> — no account, no scheduler, no password. For a recurring pull, make
        an account and a scheduler instead.
      </div>

      <ErrorBox error={error} />

      <div className="row">
        <Field label="Moodle address" hint="Only the part before the first slash.">
          <input
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.target.value })}
            placeholder="https://elearning.dhbw-ravensburg.de"
          />
        </Field>
        <Field label="Your Moodle user name">
          <input value={form.user} onChange={(e) => setForm({ ...form, user: e.target.value })} />
        </Field>
      </div>

      <Field label="Your Moodle password" hint="Sent once, stored nowhere.">
        <input
          type="password"
          value={form.password}
          onChange={(e) => setForm({ ...form, password: e.target.value })}
          onKeyDown={(e) => {
            if (e.key === "Enter" && form.url && form.user && form.password) void pull();
          }}
        />
      </Field>

      <Field
        label="Target folder"
        hint="Empty means the project itself — no extra folder is created."
      >
        <input
          value={form.target}
          onChange={(e) => setForm({ ...form, target: e.target.value })}
          placeholder="e.g. moodle — or leave empty"
        />
      </Field>

      <Field label="Only these courses" hint="Short names, comma separated. Empty means all of them.">
        <input value={form.courses} onChange={(e) => setForm({ ...form, courses: e.target.value })} />
      </Field>

      <Field
        label="Which course goes into which project"
        hint="One rule per line, first match wins. A bare number is the semester Moodle derives; anything else matches the course name — a piece of it is enough; * catches the rest. Empty puts everything in this project."
      >
        <textarea
          style={{ minHeight: 92, fontFamily: "var(--mono)", fontSize: 13 }}
          value={form.routes}
          onChange={(e) => setForm({ ...form, routes: e.target.value })}
          placeholder={"Grundlagen In -> semester1\n2 -> semester2\n* -> moodle-archiv"}
        />
      </Field>

      <label className="check">
        <input
          type="checkbox"
          checked={form.onlyCurrent}
          onChange={(e) => setForm({ ...form, onlyCurrent: e.target.checked })}
        />
        <span>Only courses that are still running</span>
      </label>
      <label className="check">
        <input type="checkbox" checked={form.flat} onChange={(e) => setForm({ ...form, flat: e.target.checked })} />
        <span>No folders at all — every file straight into the target folder</span>
      </label>
      <label className="check">
        <input type="checkbox" checked={form.prune} onChange={(e) => setForm({ ...form, prune: e.target.checked })} />
        <span>Remove files Moodle no longer has</span>
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.rebuild}
          onChange={(e) => setForm({ ...form, rebuild: e.target.checked })}
        />
        <span>Rebuild — fetch everything again, not only what is new</span>
      </label>
      <p className="hint" style={{ marginTop: -6 }}>Only inside the folders this pull writes into.</p>

      <div style={{ marginTop: 14 }}>
        <button
          className="btn primary"
          disabled={busy || !form.url || !form.user || !form.password}
          onClick={pull}
        >
          <Icon name="download" size={15} /> {busy ? "pulling…" : "Pull once"}
        </button>
      </div>

      {result ? (
        <div style={{ marginTop: 16 }}>
          <div className="notice">
            {result.message} — {result.files} file(s) written.
          </div>
          {result.log.length ? <pre className="block">{result.log.join("\n")}</pre> : null}
        </div>
      ) : null}
    </div>
  );
}
