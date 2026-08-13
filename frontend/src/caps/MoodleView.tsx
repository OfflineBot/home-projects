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
    onlyCurrent: true,
    flat: false,
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
        <strong>This pull stores nothing.</strong> The password is used for one sign-in and then forgotten —
        no account is created, no scheduler is left behind. If it goes wrong, simply type it again. For a
        nightly pull, make an account under <em>Accounts</em> and a scheduler on this project; that password
        is single-use, this one is not stored at all.
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

      <Field label="Your Moodle password" hint="Sent once, kept nowhere.">
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
        <span>No folder per course — everything straight into the target folder</span>
      </label>

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
