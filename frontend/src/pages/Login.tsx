import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Field } from "../components/ui";
import { ApiError, login } from "../lib/api";
import { setUser, useSession } from "../lib/store";

export default function Login() {
  const session = useSession();
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [needsTotp, setNeedsTotp] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (session.user) {
    return (
      <div className="empty">
        You are signed in as <strong>{session.user.username}</strong>.
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 380, margin: "8vh auto" }}>
      <h1 style={{ marginTop: 0 }}>Sign in</h1>
      <p style={{ color: "var(--ctp-subtext0)" }}>
        Everything public stays readable without signing in — this is for the rest.
      </p>

      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
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
            autoComplete="current-password"
          />
        </Field>
        {needsTotp ? (
          <Field label="Code from your authenticator">
            <input value={totp} onChange={(e) => setTotp(e.target.value)} inputMode="numeric" autoFocus />
          </Field>
        ) : null}

        <button className="btn primary" style={{ width: "100%" }} disabled={busy || !username || !password}>
          {busy ? "…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
