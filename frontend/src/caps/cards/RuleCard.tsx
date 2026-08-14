import { useState } from "react";
import { Icon } from "../../components/Icon";
import { api } from "../../lib/api";
import type { CardProps } from "../../components/board/cards";

/**
 * A button that runs a rule.
 *
 * The rule lives in a project's automation, where it can be edited and
 * versioned like everything else; this is only the press. What it printed shows
 * underneath, because a button that does something invisibly is a button nobody
 * trusts twice.
 */
export default function RuleCard({ options }: CardProps) {
  const project = String(options.projectId ?? "");
  const rule = String(options.rule ?? "");
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState("");
  const [failed, setFailed] = useState(false);

  if (!project || !rule) return <div className="meta">This card has no rule yet.</div>;

  return (
    <div className="card-rule">
      <button
        className="btn primary"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setFailed(false);
          setSaid("");
          try {
            const run = await api<{ run?: { status?: string; message?: string } }>(
              `/api/projects/${project}/automation/rules/${encodeURIComponent(rule)}/run`,
              { body: {} },
            );
            setSaid(run.run?.message || run.run?.status || "done");
            setFailed(run.run?.status === "error");
          } catch (err) {
            setSaid((err as Error).message);
            setFailed(true);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Icon name="play" size={15} /> {options.title || rule}
      </button>
      {said ? <div className={failed ? "meta bad" : "meta"}>{said}</div> : null}
    </div>
  );
}
