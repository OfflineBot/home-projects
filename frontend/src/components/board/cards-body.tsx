import { Suspense } from "react";
import { Spinner } from "../ui";
import { cardViews } from "./cards";
import type { Project, Variable } from "../../lib/api";
import type { Card } from "./Board";
import type { CardStyle } from "./Board";

/**
 * One card, drawn.
 *
 * Its own module because more than one layout draws cards: the grid, the flow,
 * the free surface and the sections all end here, and a card should look the
 * same in all of them.
 */

/** What a card's chosen look means in the page. */
export function dress(style?: CardStyle) {
  const s = style ?? {};
  return {
    className: [
      "card",
      s.background === "tinted" ? "tinted" : "",
      s.background === "bare" ? "bare" : "",
      s.border === false ? "borderless" : "",
      s.size === "large" ? "large" : "",
      s.align === "center" ? "centred" : "",
    ]
      .filter(Boolean)
      .join(" "),
    style: s.color ? ({ ["--card-color" as string]: `var(--ctp-${s.color})` } as const) : undefined,
  };
}

export function CardBody(props: {
  card: Card;
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  editing: boolean;
}) {
  const look = dress(props.card.style);
  return (
    <div className={`${look.className} card-${props.card.kind}`} style={look.style}>
      <CardInner {...props} />
    </div>
  );
}

export function CardInner({
  card,
  value,
  projects,
  editing,
}: {
  card: Card;
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  editing: boolean;
}) {
  const View = cardViews[card.kind];
  return (
    <Suspense fallback={<Spinner />}>
      {View ? (
        <View options={card.options ?? {}} value={value} projects={projects} editing={editing} />
      ) : (
        <div className="meta">No card of kind “{card.kind}” is installed.</div>
      )}
    </Suspense>
  );
}
