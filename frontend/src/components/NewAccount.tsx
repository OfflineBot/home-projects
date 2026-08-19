import { useState } from "react";
import { api, type AccountKind } from "../lib/api";
import { ErrorBox, Field, Modal, useGuarded } from "./ui";
import { Fields } from "./Fields";

/**
 * Making an account, wherever the need for one comes up.
 *
 * It lives here rather than on the accounts page because the moment a person
 * notices they need one is usually somewhere else — halfway through setting up
 * a scheduler that asks for a login.
 */
export default function NewAccount({
  kinds,
  only,
  onClose,
  onCreated,
}: {
  kinds: AccountKind[];
  /** When the need came from somewhere that knows what it wants, offer that. */
  only?: string[];
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const guarded = useGuarded();
  const offered = only?.length ? kinds.filter((k) => only.includes(k.name)) : kinds;
  const [kindName, setKindName] = useState(offered[0]?.name ?? "");
  const [title, setTitle] = useState("");
  const [config, setConfig] = useState<Record<string, any>>({});
  const [secret, setSecret] = useState("");
  const [provider, setProvider] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const kind = offered.find((k) => k.name === kindName);

  return (
    <Modal
      title="New account"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            onClick={async () => {
              try {
                const made = await guarded("storing credentials", () =>
                  api<{ id: string }>("/api/accounts", { body: { kind: kindName, title, config, secret } }),
                );
                onCreated(made.id);
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Save
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Kind">
        <select value={kindName} onChange={(e) => setKindName(e.target.value)}>
          {offered.map((k) => (
            <option key={k.name} value={k.name}>
              {k.title}
            </option>
          ))}
        </select>
      </Field>
      {kind?.description ? <p className="hint">{kind.description}</p> : null}
      {kind?.providers?.length ? (
        <Field label="Provider" hint="Fills in the servers and ports. Only the user name is left.">
          <select
            value={provider}
            onChange={(e) => {
              const p = kind.providers?.find((x) => x.name === e.target.value);
              setProvider(e.target.value);
              if (p) {
                setConfig({ ...config, ...p.fields });
                if (!title.trim()) setTitle(p.title);
              }
            }}
          >
            <option value="">Somewhere else</option>
            {kind.providers.map((p) => (
              <option key={p.name} value={p.name}>
                {p.title}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      {kind?.providers?.find((p) => p.name === provider)?.note ? (
        <p className="hint">{kind.providers.find((p) => p.name === provider)?.note}</p>
      ) : null}
      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={kind?.title} />
      </Field>
      {/* One renderer for every setting there is — see components/Fields. */}
      <Fields specs={kind?.fields ?? []} values={config} onChange={setConfig} />

      {kind?.secretLabel ? (
        <Field
          label={kind.secretLabel}
          hint={
            kind.secretIsKey
              ? "A key, not a password — a failed connection does not consume it."
              : "Single-use: a failed attempt deletes it and it has to be typed in again."
          }
        >
          {kind.secretIsKey ? (
            <textarea value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
          ) : (
            <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} />
          )}
        </Field>
      ) : null}
    </Modal>
  );
}
