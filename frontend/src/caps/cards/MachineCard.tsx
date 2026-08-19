import { useState } from "react";
import { Icon } from "../../components/Icon";
import { Spinner, useAsk } from "../../components/ui";
import { api } from "../../lib/api";
import { useQuery } from "../../lib/store";
import type { CardProps } from "../../components/board/cards";

/**
 * A machine on a board: is it on, and the two buttons that belong to that.
 *
 * This is the card the whole board exists for — "wake the PC" is a thing you
 * want on a page, not three clicks into a project. Waking needs no credential;
 * shutting down does, and a machine that has an account behind it is never
 * asked for one.
 */
export default function MachineCard({ options }: CardProps) {
  const ask = useAsk();
  const project = String(options.projectId ?? "");
  const name = String(options.machine ?? "");
  const { data, loading, reload } = useQuery<{
    machines: { name: string; host: string; up: boolean; mac?: string; account?: string }[];
  }>(project ? `/api/projects/${project}/machines` : null);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");

  const machine = (data?.machines ?? []).find((m) => m.name === name) ?? data?.machines?.[0];
  if (!project || !name) return <div className="meta">This card has no machine yet.</div>;
  if (loading && !data) return <Spinner />;
  if (!machine) return <div className="meta">There is no machine called “{name}” any more.</div>;

  const base = `/api/projects/${project}/machines/${encodeURIComponent(machine.name)}`;

  const power = async (what: "shutdown" | "reboot") => {
    const password = machine.account
      ? ""
      : (await ask.text({ title: machine.name, label: "SSH password", secret: true })) ?? "";
    if (!machine.account && !password) return;
    setBusy(what);
    setNote("");
    try {
      await api(`${base}/power`, { body: { what, password } });
      setNote(what === "reboot" ? "restarting…" : "going down…");
      setTimeout(reload, 5000);
    } catch (err) {
      setNote((err as Error).message);
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="card-machine">
      <div className="card-machine-top">
        <span className={machine.up ? "dot-status on" : "dot-status off"} />
        <strong>{options.title || machine.name}</strong>
        <span className="meta">{machine.up ? "up" : "not answering"}</span>
      </div>
      <div className="card-machine-foot">
        {/* All three, always. They used to appear according to whether the
            machine answered a port check — so a PC that was on but not
            answering had no way to be shut down from its own card, while the
            machines page offered it. What a button does is not a matter of
            what a check thinks. */}
        {machine.mac ? (
          <button
            className="btn small primary"
            disabled={busy === "wake"}
            onClick={async () => {
              setBusy("wake");
              setNote("");
              try {
                await api(`${base}/wake`, { body: {} });
                setNote("packet sent");
                setTimeout(reload, 8000);
              } catch (err) {
                setNote((err as Error).message);
              } finally {
                setBusy("");
              }
            }}
          >
            <Icon name="zap" size={14} /> Wake
          </button>
        ) : null}
        <button className="btn small ghost" disabled={busy !== ""} onClick={() => power("shutdown")}>
          <Icon name="lock" size={13} /> Shut down
        </button>
        <button className="btn small ghost" disabled={busy !== ""} onClick={() => power("reboot")}>
          <Icon name="refresh" size={13} /> Restart
        </button>
        <button className="btn ghost icon" aria-label="Check again" onClick={reload}>
          <Icon name="refresh" size={13} />
        </button>
      </div>
      {note ? <div className="meta">{note}</div> : null}
    </div>
  );
}
