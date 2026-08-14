import { lazy, type ComponentType, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../Icon";
import { formatDate } from "../ui";
import { type Project, type Variable } from "../../lib/api";

/**
 * What a card can be.
 *
 * The registry is the mirror of the backend's: the core knows a handful, and
 * every capability adds its own beside them. A card gets its options, a way to
 * look up a variable, and nothing else — it cannot reach into the board.
 */

export interface CardProps {
  options: Record<string, any>;
  /** The value of "project-slug.name" inside a group, or of a group's own. */
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  editing: boolean;
}

export type CardView = ComponentType<CardProps>;

// ------------------------------------------------------------- the core ones

function TextCard({ options }: CardProps) {
  // Markdown, in the small way this needs: paragraphs, bold, links, lists.
  const text = String(options.text ?? "");
  return <div className="card-text">{renderMarkdown(text)}</div>;
}

function LinkCard({ options }: CardProps) {
  const lines = String(options.links ?? "")
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [title, address] = line.includes("|") ? line.split("|") : [line, line];
      return { title: title.trim(), address: (address ?? title).trim() };
    });
  return (
    <div className="card-links">
      {lines.map((l, i) =>
        l.address.startsWith("/") ? (
          <Link key={i} to={l.address} className="card-link">
            <Icon name="link" size={14} /> {l.title}
          </Link>
        ) : (
          <a key={i} href={l.address} className="card-link" target="_blank" rel="noreferrer">
            <Icon name="link" size={14} /> {l.title}
          </a>
        ),
      )}
      {lines.length === 0 ? <span className="meta">No links yet.</span> : null}
    </div>
  );
}

function HeadingCard({ options }: CardProps) {
  return <h2 className="card-heading">{String(options.title ?? "")}</h2>;
}

function NumberCard({ options, value }: CardProps) {
  const v = value(String(options.variable ?? ""), options.groupId);
  return (
    <div className="card-number">
      <div className="stat">
        {v ? format(v.value) : "—"}
        {v?.unit ? <span className="unit">{v.unit}</span> : null}
      </div>
      <div className="meta">{options.title || shortName(String(options.variable ?? ""))}</div>
      {v?.updatedAt ? <div className="meta card-when">{formatDate(v.updatedAt)}</div> : null}
    </div>
  );
}

function StatusCard({ options, value }: CardProps) {
  const v = value(String(options.variable ?? ""), options.groupId);
  const on = Boolean(v?.value);
  return (
    <div className="card-status">
      <span className={on ? "dot-status on" : "dot-status off"} />
      <div>
        <div className="stat" style={{ fontSize: 18 }}>{on ? "on" : "off"}</div>
        <div className="meta">{options.title || shortName(String(options.variable ?? ""))}</div>
      </div>
    </div>
  );
}

function ListCard({ options, value }: CardProps) {
  const v = value(String(options.variable ?? ""), options.groupId);
  const items = Array.isArray(v?.value) ? (v!.value as any[]) : [];
  return (
    <div className="card-list">
      <div className="meta">{options.title || shortName(String(options.variable ?? ""))}</div>
      <ul>
        {items.slice(0, 12).map((item, i) => (
          <li key={i}>{typeof item === "object" ? JSON.stringify(item) : String(item)}</li>
        ))}
        {items.length === 0 ? <li className="meta">nothing</li> : null}
      </ul>
    </div>
  );
}

function ProjectCard({ options, projects, value }: CardProps) {
  const project = projects.find((p) => p.id === options.projectId);
  if (!project) return <div className="meta">That project is gone.</div>;
  const address = `/groups/${project.groupSlug}/${project.slug}`;
  const numbers = (options.numbers ? String(options.numbers).split(",") : [])
    .map((name) => value(name.trim(), options.groupId))
    .filter(Boolean) as Variable[];
  return (
    <div className="card-project">
      <Link to={address} className="card-project-name">
        <Icon name={project.icon} size={16} /> {options.title || project.title}
      </Link>
      <div className="meta">{project.groupTitle ?? project.groupSlug}</div>
      {numbers.length ? (
        <div className="tile-numbers">
          {numbers.map((v) => (
            <div key={v.name}>
              <div className="stat" style={{ fontSize: 20 }}>{format(v.value)}</div>
              <div className="meta">{v.name}</div>
            </div>
          ))}
        </div>
      ) : null}
      <div className="card-project-foot">
        <Link className="btn small" to={address}>Open</Link>
        <Link className="btn small ghost" to={`${address}/files`}>Files</Link>
      </div>
    </div>
  );
}

function HistoryCard({ options, value }: CardProps) {
  // The graph itself lives in the dashboard's history component; here the
  // current value with its name is enough to be useful, and the graph is drawn
  // by the capability card when there is one.
  const v = value(String(options.variable ?? ""), options.groupId);
  return (
    <div className="card-number">
      <div className="stat">{v ? format(v.value) : "—"}</div>
      <div className="meta">{options.title || shortName(String(options.variable ?? ""))}</div>
    </div>
  );
}

// --------------------------------------------------------------- the registry

export const cardViews: Record<string, CardView> = {
  text: TextCard,
  link: LinkCard,
  heading: HeadingCard,
  number: NumberCard,
  status: StatusCard,
  list: ListCard,
  project: ProjectCard,
  history: HistoryCard,
  // A capability's cards are loaded only when one is actually on a board.
  machine: lazy(() => import("../../caps/cards/MachineCard")),
  terminal: lazy(() => import("../../caps/cards/TerminalCard")),
  rule: lazy(() => import("../../caps/cards/RuleCard")),
  agenda: lazy(() => import("../../caps/cards/AgendaCard")),
  "links-list": lazy(() => import("../../caps/cards/LinksCard")),
};

// ------------------------------------------------------------------- helpers

function shortName(variable: string) {
  const [, rest] = variable.includes(".") ? variable.split(".") : ["", variable];
  return rest || variable;
}

export function format(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "number") return Number.isInteger(value) ? String(value) : value.toFixed(2);
  if (typeof value === "boolean") return value ? "yes" : "no";
  if (Array.isArray(value)) return String(value.length);
  return String(value);
}

/** Markdown in the amount a card needs: headings, bold, links, lists. */
function renderMarkdown(text: string) {
  return text.split("\n").map((line, i) => {
    if (line.startsWith("## ")) return <h3 key={i}>{line.slice(3)}</h3>;
    if (line.startsWith("# ")) return <h2 key={i}>{line.slice(2)}</h2>;
    if (line.startsWith("- ")) return <li key={i}>{inline(line.slice(2))}</li>;
    if (!line.trim()) return <br key={i} />;
    return <p key={i}>{inline(line)}</p>;
  });
}

function inline(text: string) {
  // **bold** and [title](address), which is what a note on a board uses.
  const parts: ReactNode[] = [];
  let rest = text;
  let key = 0;
  const pattern = /\*\*(.+?)\*\*|\[(.+?)\]\((.+?)\)/;
  for (;;) {
    const match = pattern.exec(rest);
    if (!match) break;
    parts.push(rest.slice(0, match.index));
    if (match[1]) parts.push(<strong key={key++}>{match[1]}</strong>);
    else
      parts.push(
        match[3].startsWith("/") ? (
          <Link key={key++} to={match[3]}>{match[2]}</Link>
        ) : (
          <a key={key++} href={match[3]} target="_blank" rel="noreferrer">{match[2]}</a>
        ),
      );
    rest = rest.slice(match.index + match[0].length);
  }
  parts.push(rest);
  return parts;
}
