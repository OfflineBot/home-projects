import { useMemo } from "react";
import { useQuery } from "../lib/store";
import { colorVar } from "../lib/theme";

interface Node {
  id: string;
  slug: string;
  title: string;
  group?: string;
  color?: string;
  external?: boolean;
}

interface Edge {
  from: string;
  to: string;
  kind: "link" | "serves" | "writes";
  label?: string;
}

const MEANING: Record<Edge["kind"], string> = {
  link: "shows",
  serves: "serves",
  writes: "writes into",
};

/**
 * What depends on what, in one group.
 *
 * The nodes sit on a circle because a group has a handful of projects, not a
 * hundred, and a circle needs no layout engine to stay readable. An arrow that
 * leaves the group keeps its node, drawn dimmer: those are the ones that break
 * when something moves.
 */
export default function Graph({ group }: { group: string }) {
  const { data } = useQuery<{ nodes: Node[]; edges: Edge[] }>(`/api/groups/${group}/graph`);

  const placed = useMemo(() => {
    const nodes = data?.nodes ?? [];
    const radius = nodes.length <= 2 ? 0 : 130;
    return nodes.map((n, i) => {
      const angle = (i / Math.max(1, nodes.length)) * Math.PI * 2 - Math.PI / 2;
      return { ...n, x: 200 + Math.cos(angle) * radius, y: 170 + Math.sin(angle) * radius };
    });
  }, [data]);

  if (!data || data.edges.length === 0) return null;

  const at = (id: string) => placed.find((p) => p.id === id);

  return (
    <div className="graph">
      <svg viewBox="0 0 400 340" width="100%" height="340">
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--ctp-overlay1)" />
          </marker>
        </defs>
        {data.edges.map((e, i) => {
          const a = at(e.from);
          const b = at(e.to);
          if (!a || !b) return null;
          return (
            <g key={i}>
              <line
                x1={a.x}
                y1={a.y}
                x2={b.x}
                y2={b.y}
                stroke="var(--ctp-overlay0)"
                strokeWidth={1.2}
                strokeDasharray={e.kind === "writes" ? "4 3" : undefined}
                markerEnd="url(#arrow)"
              />
              <title>{`${a.slug} ${MEANING[e.kind]} ${b.slug}${e.label ? ` (${e.label})` : ""}`}</title>
            </g>
          );
        })}
        {placed.map((n) => (
          <g key={n.id} opacity={n.external ? 0.55 : 1}>
            <circle cx={n.x} cy={n.y} r={7} fill={colorVar(n.color)} />
            <text x={n.x} y={n.y - 12} textAnchor="middle" fill="var(--ctp-subtext0)" fontSize="11">
              {n.slug}
              {n.external ? ` (${n.group || "elsewhere"})` : ""}
            </text>
          </g>
        ))}
      </svg>
      <div className="graph-key">
        {[...new Set(data.edges.map((e) => e.kind))].map((k) => (
          <span key={k} className="meta">
            {MEANING[k]}
          </span>
        ))}
      </div>
    </div>
  );
}
