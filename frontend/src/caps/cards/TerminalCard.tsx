import { Terminal } from "../../components/terminal";
import { Spinner } from "../../components/ui";
import { useQuery } from "../../lib/store";
import type { CardProps } from "../../components/board/cards";

/** A tmux session on a board. The work is all in <Terminal>. */
export default function TerminalCard({ options, editing }: CardProps) {
  const project = String(options.projectId ?? "");
  const machine = String(options.machine ?? "");
  const { data, loading } = useQuery<{ machines: { name: string; account?: string }[] }>(
    project ? `/api/projects/${project}/machines` : null,
  );
  if (!project || !machine) return <div className="meta">This card has no machine yet.</div>;
  // Nothing is drawn until it is known whether this machine has an account.
  // Drawn earlier, the terminal believes there is none, asks for a password
  // and keeps asking after the answer has arrived — a machine that signs
  // itself in was demanding one.
  if (loading && !data) return <Spinner />;

  const known = (data?.machines ?? []).find((m) => m.name === machine);
  return (
    <Terminal
      base={`/api/projects/${project}/machines/${encodeURIComponent(machine)}`}
      machine={machine}
      session={String(options.session ?? "") || undefined}
      byAccount={Boolean(known?.account)}
      asButton={String(options.as ?? "") === "button"}
      editing={editing}
      title={options.title ? String(options.title) : undefined}
    />
  );
}
