import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Field } from "../components/ui";
import { ApiError, api, login } from "../lib/api";
import { setUser, useSession } from "../lib/store";

export default function Login() {
  const session = useSession();
  const navigate = useNavigate();
  const [asking, setAsking] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [note, setNote] = useState("");
  const [totp, setTotp] = useState("");
  const [needsTotp, setNeedsTotp] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [busy, setBusy] = useState(false);

  if (session.user) {
    return (
      <div className="empty">
        You are signed in as <strong>{session.user.username}</strong>.
      </div>
    );
  }

  if (waiting) {
    return (
      <div style={{ maxWidth: 380, margin: "8vh auto" }}>
        <h1 style={{ marginTop: 0 }}>Asked for</h1>
        <p style={{ color: "var(--ctp-subtext0)" }}>
          The account <strong>{username}</strong> exists but opens nothing yet. It works once the owner
          has let it in.
        </p>
        <button className="btn" onClick={() => { setWaiting(false); setAsking(false); setPassword(""); }}>
          Back to sign in
        </button>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 380, margin: "8vh auto" }}>
      <h1 style={{ marginTop: 0 }}>{asking ? "Ask for an account" : "Sign in"}</h1>
      <p style={{ color: "var(--ctp-subtext0)" }}>
        {asking
          ? "The owner lets accounts in by hand. What you make afterwards is yours alone."
          : "Everything public stays readable without signing in — this is for the rest."}
      </p>

      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
            if (asking) {
              await api("/api/auth/register", { body: { username, password, note } });
              setWaiting(true);
              return;
            }
            const user = await login(username, password, totp || undefined);
            setUser(user);
            // After signing in you stay where you were.
            navigate(-1);
          } catch (err) {
            if (err instanceof ApiError && err.code === "totp_required") {
              setNeedsTotp(true);
              setError("This account asks for a second factor.");
            } else {
              setError(err instanceof Error ? err.message : String(err));
            }
          } finally {
            setBusy(false);
          }
        }}
      >
        {error ? (
          <div className="error">
            <Icon name="alert" />
            <div>{error}</div>
          </div>
        ) : null}

        <Field label="Username">
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus autoComplete="username" />
        </Field>
        <Field label="Password">
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={asking ? "new-password" : "current-password"}
          />
        </Field>
        {asking ? (
          <Field label="Who are you?" hint="The owner sees this when deciding.">
            <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="optional" />
          </Field>
        ) : null}
        {needsTotp ? (
          <Field label="Code from your authenticator">
            <input value={totp} onChange={(e) => setTotp(e.target.value)} inputMode="numeric" autoFocus />
          </Field>
        ) : null}

        <button className="btn primary" style={{ width: "100%" }} disabled={busy || !username || !password}>
          {busy ? "…" : asking ? "Ask" : "Sign in"}
        </button>
      </form>

      <button
        className="btn ghost small"
        style={{ marginTop: 12 }}
        onClick={() => { setAsking(!asking); setError(null); }}
      >
        {asking ? "I have an account" : "I need an account"}
      </button>
    </div>
  );
}
