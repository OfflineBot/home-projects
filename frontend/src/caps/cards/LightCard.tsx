import { useCallback, useEffect, useState } from "react";
import { Icon } from "../../components/Icon";
import { api } from "../../lib/api";
import type { CardProps } from "../../components/board/cards";

/**
 * A light, as a switch.
 *
 * A rule button says what it does; this says what the lamp is doing. It reads
 * the state when it appears and again after every press, so the board is not
 * telling a story about a light that somebody switched at the wall.
 *
 * The brightness is there when the card has room for it. On a two-column card
 * it is only the switch, which is what a light on a wall is.
 */
export default function LightCard({ options, editing }: CardProps) {
  const project = String(options.projectId ?? "");
  const host = String(options.host ?? "");
  const name = String(options.title ?? "") || host;
  const [on, setOn] = useState<boolean | null>(null);
  const [bright, setBright] = useState(128);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  const read = useCallback(async () => {
    if (!project || !host) return;
    try {
      const answer = await api<{ light: { on: boolean; brightness: number; reachable: boolean }; note?: string }>(
        `/api/projects/${project}/automation/light?host=${encodeURIComponent(host)}`,
        { method: "GET" },
      );
      if (answer.light?.reachable) {
        setOn(answer.light.on);
        if (answer.light.brightness > 0) setBright(answer.light.brightness);
        setNote("");
      } else {
        setOn(null);
        setNote(answer.note || "not answering");
      }
    } catch (err) {
      setOn(null);
      setNote((err as Error).message);
    }
  }, [project, host]);

  useEffect(() => {
    void read();
    // A lamp can be switched at the wall too, so the card looks again now and
    // then rather than believing its own last press forever.
    const timer = setInterval(() => void read(), 30_000);
    return () => clearInterval(timer);
  }, [read]);

  const send = async (body: Record<string, unknown>) => {
    setBusy(true);
    try {
      const answer = await api<{ light: { on: boolean; brightness: number; reachable: boolean }; note?: string }>(
        `/api/projects/${project}/automation/light`,
        { body: { host, ...body } },
      );
      if (answer.light?.reachable) {
        setOn(answer.light.on);
        if (answer.light.brightness > 0) setBright(answer.light.brightness);
        setNote("");
      } else setNote(answer.note || "not answering");
    } catch (err) {
      setNote((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (!project || !host) return <div className="meta">This card has no light yet.</div>;

  return (
    <div className={on ? "card-light lit" : "card-light"}>
      <button
        className="light-switch"
        disabled={busy || editing}
        aria-pressed={on === true}
        title={editing ? "Leave edit mode to switch it" : `Switch ${name}`}
        onClick={() => void send({ power: "toggle" })}
      >
        <Icon name="lightbulb" size={18} />
        <span className="grow">{name}</span>
        <span className="light-state">{on === null ? "—" : on ? "on" : "off"}</span>
      </button>
      <input
        className="light-bright"
        type="range"
        min={1}
        max={255}
        value={bright}
        disabled={busy || editing}
        aria-label="Brightness"
        onChange={(e) => setBright(Number(e.target.value))}
        onPointerUp={() => void send({ brightness: bright })}
        onKeyUp={(e) => {
          if (e.key.startsWith("Arrow")) void send({ brightness: bright });
        }}
      />
      {note ? <div className="meta bad">{note}</div> : null}
    </div>
  );
}
