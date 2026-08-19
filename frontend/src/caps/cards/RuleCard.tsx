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
  // What it said used to be a line underneath, so every press pushed the page
  // about. It is a mark over the button now, and it can be switched off.
  const feedback = String(options.feedback ?? "brief");
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState("");
  const [failed, setFailed] = useState(false);

  if (!project || !rule) return <div className="meta">This card has no rule yet.</div>;

  if (feedback === "none") {
    // Nothing to say: the lamp is the feedback.
    return (
      <div className="card-rule">
        <button
          className="btn primary"
          disabled={busy}
          onClick={() => {
            void api(`/api/projects/${project}/automation/rules/${encodeURIComponent(rule)}/run`, { body: {} }).catch(
              () => {},
            );
          }}
        >
          <Icon name="play" size={15} /> {options.title || rule}
        </button>
      </div>
    );
  }

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
      {said ? (
        <span className={failed ? "said bad" : "said"} title={said}>
          {failed ? said : "✓"}
        </span>
      ) : null}
    </div>
  );
}
