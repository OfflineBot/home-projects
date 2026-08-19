import { useCallback, useEffect, useState } from "react";
import { Icon } from "../../components/Icon";
import { api } from "../../lib/api";
import type { CardProps } from "../../components/board/cards";

interface Waiting {
  id: number;
  rule: string;
  dueAt: string;
}

/**
 * A number, a button, and what is coming.
 *
 * "Everything on in five minutes" is a thing you ask for standing in the hall
 * with your coat on, so it is a box you type a number into and one button. What
 * has been asked for is listed underneath with the time it will happen, and
 * there is one button to call all of it off — which is what anybody wants at
 * the moment they realise they pressed it too early.
 */
export default function TimerCard({ options, editing }: CardProps) {
  const project = String(options.projectId ?? "");
  const rule = String(options.rule ?? "");
  const suggested = Number(options.minutes) || 5;
  // A button that says how long is a button somebody can press without
  // reading: "everything on in 20 minutes", and another one for five.
  const ask = String(options.ask ?? "yes") !== "no";
  const feedback = String(options.feedback ?? "brief") !== "none";
  const [minutes, setMinutes] = useState(String(suggested));
  const [waiting, setWaiting] = useState<Waiting[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  const look = useCallback(async () => {
    if (!project) return;
    try {
      const answer = await api<{ waiting: Waiting[] }>(`/api/projects/${project}/automation/later`);
      setWaiting(answer.waiting ?? []);
    } catch {
      /* a card that cannot count down is still a button */
    }
  }, [project]);

  useEffect(() => {
    void look();
    // The list is a countdown: it has to move without being asked.
    const timer = setInterval(() => void look(), 15_000);
    return () => clearInterval(timer);
  }, [look]);

  if (!project || !rule) return <div className="meta">This card has no rule yet.</div>;

  const start = async () => {
    setBusy(true);
    setNote("");
    try {
      await api(`/api/projects/${project}/automation/later`, {
        body: { rule, minutes: ask ? Number(minutes) || suggested : suggested },
      });
      await look();
    } catch (err) {
      setNote((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const stopAll = async () => {
    setBusy(true);
    try {
      await api(`/api/projects/${project}/automation/later`, { method: "DELETE" });
      await look();
    } catch (err) {
      setNote((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const mine = waiting.filter((w) => w.rule === rule);

  return (
    <div className="card-timer">
      <div className="card-timer-row">
        {ask ? (
          <>
            <input
              type="number"
              min={1}
              max={1440}
              value={minutes}
              disabled={editing}
              aria-label="In how many minutes"
              onChange={(e) => setMinutes(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !editing) void start();
              }}
            />
            <span className="meta">min</span>
          </>
        ) : null}
        <button className="btn primary grow" disabled={busy || editing} onClick={() => void start()}>
          <Icon name="clock" size={14} />{" "}
          {options.title || (ask ? rule : `${rule} in ${suggested} min`)}
        </button>
      </div>

      {feedback && mine.length ? (
        <div className="card-timer-waiting">
          {mine.map((w) => (
            <span key={w.id} className="badge good">
              {new Date(w.dueAt).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
            </span>
          ))}
          <button className="btn small ghost" disabled={busy} onClick={() => void stopAll()}>
            stop
          </button>
        </div>
      ) : null}
      {note ? <div className="meta bad">{note}</div> : null}
    </div>
  );
}
