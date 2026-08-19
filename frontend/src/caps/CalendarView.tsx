import { useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { ErrorBox, Field, Modal, Spinner } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";
import { colorVar } from "../lib/theme";

/**
 * A real calendar, in calendar format — not a file listing.
 *
 * Opening a calendar project shows the grid. The files sit behind their own
 * tab, for whoever wants them. The week starts on Monday. Changes appear
 * immediately and snap back with a reason if the server refuses them.
 *
 * Five kinds of entry live in here, and they are drawn differently on purpose:
 * a slot is a block, a deadline is a marker with weight, a phase is a band
 * behind everything else — because a six-week internship drawn as a block
 * would bury every lecture underneath it.
 */

export type Kind = "slot" | "all-day" | "deadline" | "phase" | "milestone";

export interface Occurrence {
  projectId: string;
  projectSlug?: string;
  projectTitle?: string;
  color?: string;
  uid: string;
  recurrenceId?: string;
  start: string;
  end: string;
  allDay: boolean;
  summary: string;
  description?: string;
  location?: string;
  rrule?: string;
  repeats: boolean;
  isException?: boolean;
  alarms?: number[];
  readOnly: boolean;
  sourceFile: string;
  kind: Kind;
  isTodo?: boolean;
  done?: boolean;
  completedAt?: string;
  priority?: number;
  overdue?: boolean;
  categories?: string[];
  relatedTo?: string;
  attachedTo?: string;
  link?: string;
  person?: string;
}

type View = "month" | "week" | "day" | "list";

export const KINDS: { key: Kind; title: string; hint: string }[] = [
  { key: "slot", title: "Slot", hint: "when am I where — start and end" },
  { key: "all-day", title: "All day", hint: "what is today — a whole day or several" },
  { key: "deadline", title: "Deadline", hint: "when is it due — a point, and it can be done" },
  { key: "phase", title: "Phase", hint: "what period am I in — drawn as a band" },
  { key: "milestone", title: "Milestone", hint: "when does it start — a point, nothing owed" },
];

export default function CalendarView({ project }: { project: Project; reload: () => void }) {
  // A calendar that gathers other calendars is not a different kind of
  // calendar: it is this one, with more sources. Which projects those are is
  // set in the project's settings, and each is switchable here.
  const gathered = useQuery<{ sources: Project[] }>(`/api/projects/${project.id}/sources`);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const others = (gathered.data?.sources ?? []).filter((p) => p.capabilities?.includes("calendar"));

  const sources = [
    { id: project.id, title: project.title, color: project.color, readOnly: project.readOnly },
    ...others.map((p) => ({ id: p.id, title: p.title, color: p.color, readOnly: p.readOnly })),
  ];
  const ids = sources.map((s) => s.id).join(",");

  return (
    <CalendarGrid
      sources={sources}
      endpoint={(from, to) =>
        others.length
          ? `/api/capabilities/calendar/events?from=${from}&to=${to}&projects=${ids}`
          : `/api/projects/${project.id}/calendar/events?from=${from}&to=${to}`
      }
      defaultProject={project.id}
      showSubscription={project.id}
      hidden={others.length ? hidden : undefined}
      onToggleSource={
        others.length
          ? (id) =>
              setHidden((prev) => {
                const next = new Set(prev);
                if (next.has(id)) next.delete(id);
                else next.add(id);
                return next;
              })
          : undefined
      }
    />
  );
}

export function CalendarGrid({
  sources,
  endpoint,
  defaultProject,
  showSubscription,
  extraHeader,
  hidden,
  onToggleSource,
}: {
  sources: { id: string; title: string; color?: string; readOnly?: boolean }[];
  endpoint: (from: string, to: string) => string;
  defaultProject?: string;
  showSubscription?: string;
  extraHeader?: React.ReactNode;
  hidden?: Set<string>;
  onToggleSource?: (id: string) => void;
}) {
  const [params, setParams] = useSearchParams();
  const view = (params.get("view") as View) ?? "month";
  const anchor = params.get("at") ? new Date(params.get("at")!) : new Date();
  // The filter lives in the URL, so the back button and a shared link both
  // work. Empty means everything.
  const kindFilter = new Set((params.get("kinds") ?? "").split(",").filter(Boolean) as Kind[]);
  const tagFilter = params.get("tag") ?? "";
  // One phase with everything belonging to it — the answer to "what is in this
  // semester", without a second page.
  const phaseFilter = params.get("phase") ?? "";

  const patch = (changes: Record<string, string>) => {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(changes)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    setParams(p);
  };

  const setView = (next: View) => {
    patch({ view: next });
    // The last choice is remembered on the server too, so the next visit opens
    // the same way.
    if (showSubscription) {
      void api(`/api/projects/${showSubscription}/calendar/settings`, {
        method: "PUT",
        body: { lastView: next },
      }).catch(() => undefined);
    }
  };
  const setAnchor = (d: Date) => patch({ at: isoDate(d) });
  const toggleKind = (k: Kind) => {
    const next = new Set(kindFilter);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    patch({ kinds: [...next].join(",") });
  };

  const range = useMemo(() => rangeFor(view, anchor), [view, anchor.getTime()]);
  const { data, error, loading, reload } = useQuery<{ events: Occurrence[] }>(
    endpoint(isoDate(range.from), isoDate(range.to)),
    [range.from.getTime(), range.to.getTime()],
  );

  const [optimistic, setOptimistic] = useState<Record<string, Partial<Occurrence>>>({});
  const [editing, setEditing] = useState<Partial<Occurrence> | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);
  const [subscription, setSubscription] = useState<string | null>(null);

  const all = (data?.events ?? [])
    .filter((e) => !hidden?.has(e.projectId))
    .map((e) => ({ ...e, kind: e.kind ?? "slot", ...(optimistic[keyOf(e)] ?? {}) }));

  const events = all.filter(
    (e) =>
      (kindFilter.size === 0 || kindFilter.has(e.kind)) &&
      (!tagFilter || (e.categories ?? []).includes(tagFilter)) &&
      (!phaseFilter || e.relatedTo === phaseFilter || e.uid === phaseFilter),
  );
  const focused = phaseFilter ? all.find((e) => e.uid === phaseFilter) : undefined;
  const tags = [...new Set(all.flatMap((e) => e.categories ?? []))].sort();

  // Phases are the backdrop and milestones are pins: neither belongs in a grid
  // cell, both belong in their own lane above it.
  const phases = events.filter((e) => e.kind === "phase");
  const milestones = events.filter((e) => e.kind === "milestone");
  const inGrid = events.filter((e) => e.kind !== "phase" && e.kind !== "milestone");
  const deadlines = all.filter((e) => e.kind === "deadline");

  // Move an event by dropping it. It jumps immediately; if the server says no,
  // it snaps back and says why.
  const moveTo = async (event: Occurrence, start: Date) => {
    if (event.readOnly) {
      setActionError(new Error("This event comes from a subscription and cannot be moved."));
      return;
    }
    const length = new Date(event.end).getTime() - new Date(event.start).getTime();
    const end = new Date(start.getTime() + length);
    const key = keyOf(event);
    setOptimistic((o) => ({ ...o, [key]: { start: start.toISOString(), end: end.toISOString() } }));
    setActionError(null);
    try {
      await api(`/api/projects/${event.projectId}/calendar/events/${encodeURIComponent(event.uid)}`, {
        method: "PATCH",
        body: {
          summary: event.summary,
          description: event.description,
          location: event.location,
          start: start.toISOString(),
          end: end.toISOString(),
          allDay: event.allDay,
          rrule: event.rrule,
          kind: event.kind,
          priority: event.priority,
          categories: event.categories,
          relatedTo: event.relatedTo,
          link: event.link,
          person: event.person,
          scope: event.repeats ? "single" : "all",
          recurrenceId: event.recurrenceId,
        },
      });
      reload();
    } catch (err) {
      setOptimistic((o) => {
        const next = { ...o };
        delete next[key];
        return next;
      });
      setActionError(err as Error);
    } finally {
      setTimeout(() => setOptimistic({}), 400);
    }
  };

  const toggleDone = async (event: Occurrence) => {
    const key = keyOf(event);
    setOptimistic((o) => ({ ...o, [key]: { done: !event.done } }));
    try {
      await api(`/api/projects/${event.projectId}/calendar/events/${encodeURIComponent(event.uid)}/done`, {
        method: "POST",
        body: { done: !event.done },
      });
      reload();
    } catch (err) {
      setActionError(err as Error);
    } finally {
      setTimeout(() => setOptimistic({}), 400);
    }
  };

  const newEntry = (kind: Kind, day?: Date, hour?: number) => {
    const base = day ?? anchor;
    const start = kind === "deadline" ? atTime(base, 23, 59) : atHour(base, hour ?? 9);
    setEditing({
      projectId: lastUsedProject(sources.map((s) => s.id)) ?? defaultProject ?? sources[0]?.id,
      kind,
      start: start.toISOString(),
      end: (kind === "deadline" || kind === "milestone"
        ? start
        : new Date(start.getTime() + 3600_000)
      ).toISOString(),
      allDay: kind === "all-day" || kind === "phase" || kind === "milestone",
      summary: "",
    });
  };

  return (
    <div>
      <div className="cal-head">
        <button className="btn icon" onClick={() => setAnchor(step(view, anchor, -1))} aria-label="Back">
          <Icon name="chevronLeft" />
        </button>
        <button className="btn icon" onClick={() => setAnchor(step(view, anchor, 1))} aria-label="Forward">
          <Icon name="chevronRight" />
        </button>
        <button className="btn small" onClick={() => setAnchor(new Date())}>
          Today
        </button>
        <span className="title">{titleFor(view, anchor)}</span>
        <PhaseBadge phases={phases} />
        <div style={{ flex: 1 }} />
        {(["month", "week", "day", "list"] as View[]).map((v) => (
          <button key={v} className={v === view ? "btn small primary" : "btn small"} onClick={() => setView(v)}>
            {v}
          </button>
        ))}
        {showSubscription ? (
          <button
            className="btn small"
            onClick={async () => {
              try {
                const res = await api<{ url: string }>(`/api/projects/${showSubscription}/calendar/subscription`);
                setSubscription(res.url);
              } catch (err) {
                setActionError(err as Error);
              }
            }}
          >
            <Icon name="link" size={14} /> Subscribe
          </button>
        ) : null}
        {showSubscription ? <ImportButton project={showSubscription} onDone={reload} onFailed={setActionError} /> : null}
        <NewEntryButton onPick={(k) => newEntry(k)} />
      </div>

      {extraHeader}

      <div className="cal-filters">
        {KINDS.map((k) => (
          <button
            key={k.key}
            className={kindFilter.has(k.key) ? "badge good" : "badge"}
            style={{ cursor: "pointer", opacity: kindFilter.size && !kindFilter.has(k.key) ? 0.5 : 1 }}
            title={k.hint}
            onClick={() => toggleKind(k.key)}
          >
            {k.title}
          </button>
        ))}
        {tags.length ? (
          <select value={tagFilter} onChange={(e) => patch({ tag: e.target.value })} style={{ width: "auto" }}>
            <option value="">all tags</option>
            {tags.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        ) : null}
        {focused ? (
          <span className="badge good">
            inside {focused.summary} — {events.length - 1} entries belong to it
          </span>
        ) : null}
        {kindFilter.size || tagFilter || phaseFilter ? (
          <button className="btn small" onClick={() => patch({ kinds: "", tag: "", phase: "" })}>
            clear filter
          </button>
        ) : null}
      </div>

      {sources.length > 1 ? (
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 12 }}>
          {sources.map((s) => (
            <button
              key={s.id}
              className="badge"
              style={{
                cursor: "pointer",
                opacity: hidden?.has(s.id) ? 0.45 : 1,
                background: `color-mix(in srgb, ${colorVar(s.color)} 22%, transparent)`,
                color: colorVar(s.color),
              }}
              onClick={() => onToggleSource?.(s.id)}
            >
              <span className="dot-status" style={{ background: colorVar(s.color) }} /> {s.title}
            </button>
          ))}
        </div>
      ) : null}

      <ErrorBox error={actionError ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      <div className="cal-layout">
        <div style={{ minWidth: 0 }}>
          {view !== "list" ? (
            <PhaseLane
              phases={phases}
              milestones={milestones}
              from={range.from}
              to={range.to}
              onPick={setEditing}
              onFocus={(uid) => patch({ phase: phaseFilter === uid ? "" : uid })}
            />
          ) : null}

          {view === "month" ? (
            <MonthView
              anchor={anchor}
              events={inGrid}
              onPick={setEditing}
              onMove={moveTo}
              onNew={(day) => newEntry("slot", day)}
            />
          ) : view === "list" ? (
            <ListView events={events} onPick={setEditing} onToggleDone={toggleDone} />
          ) : (
            <TimeView
              anchor={anchor}
              days={view === "day" ? 1 : 7}
              events={inGrid}
              onPick={setEditing}
              onMove={moveTo}
              onNew={(day, hour) => newEntry("slot", day, hour)}
              showGaps={view === "day"}
            />
          )}
        </div>

        <DeadlinePanel deadlines={deadlines} onPick={setEditing} onToggleDone={toggleDone} />
      </div>

      {editing ? (
        <EventDialog
          initial={editing}
          sources={sources}
          phases={phases}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      ) : null}

      {subscription ? (
        <Modal title="Subscribe to this calendar" onClose={() => setSubscription(null)}>
          <p className="meta" style={{ marginTop: 0 }}>
            One complete VCALENDAR. No account needed to read it.
          </p>
          <input readOnly value={subscription} onFocus={(e) => e.currentTarget.select()} />
          <p className="hint">
            Deadlines leave as events. Append <code className="mono">&amp;deadlines=todos</code> for VTODO.
          </p>
        </Modal>
      ) : null}
    </div>
  );
}

// -------------------------------------------------------------- phase lane

/** Phases run behind everything else: a band per lane, never a grid cell. */
function PhaseLane({
  phases,
  milestones,
  from,
  to,
  onPick,
  onFocus,
}: {
  phases: Occurrence[];
  milestones: Occurrence[];
  from: Date;
  to: Date;
  onPick: (e: Partial<Occurrence>) => void;
  onFocus: (uid: string) => void;
}) {
  if (!phases.length && !milestones.length) return null;
  const span = to.getTime() - from.getTime();
  const pct = (d: Date) => Math.min(100, Math.max(0, ((d.getTime() - from.getTime()) / span) * 100));

  // Several phases stack into several lanes; two that overlap never share one.
  const lanes: Occurrence[][] = [];
  for (const p of [...phases].sort((a, b) => a.start.localeCompare(b.start))) {
    const lane = lanes.find((l) => l.every((o) => o.end <= p.start || o.start >= p.end));
    if (lane) lane.push(p);
    else lanes.push([p]);
  }

  return (
    <div className="cal-lanes">
      {lanes.map((lane, i) => (
        <div key={i} className="cal-lane">
          {lane.map((p) => {
            const left = pct(new Date(p.start));
            const right = pct(new Date(p.end));
            return (
              <div
                key={keyOf(p)}
                className="cal-band"
                style={{
                  left: `${left}%`,
                  width: `${Math.max(1.5, right - left)}%`,
                  ["--ev-color" as string]: colorVar(p.color),
                }}
                title={`${p.summary} · ${new Date(p.start).toLocaleDateString()} – ${new Date(p.end).toLocaleDateString()}`}
              >
                <button
                  className="name"
                  title="show everything that belongs to this phase"
                  onClick={() => onFocus(p.uid)}
                >
                  {p.summary}
                </button>
                <button className="edit" title="change this phase" onClick={() => onPick(p)}>
                  <Icon name="settings" size={12} />
                </button>
              </div>
            );
          })}
        </div>
      ))}
      {milestones.length ? (
        <div className="cal-lane pins">
          {milestones.map((m) => (
            <button
              key={keyOf(m)}
              className="cal-pin"
              style={{ left: `${pct(new Date(m.start))}%`, ["--ev-color" as string]: colorVar(m.color) }}
              title={`${m.summary} · ${new Date(m.start).toLocaleDateString()}`}
              onClick={() => onPick(m)}
            >
              <Icon name="flag" size={12} />
              <span>{m.summary}</span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/** "Semester 3 · week 7 of 20" — where you are, without counting. */
function PhaseBadge({ phases }: { phases: Occurrence[] }) {
  const now = Date.now();
  const running = phases
    .filter((p) => new Date(p.start).getTime() <= now && new Date(p.end).getTime() >= now)
    .sort((a, b) => spanOf(a) - spanOf(b))[0];
  if (!running) return null;
  const start = new Date(running.start).getTime();
  const week = Math.floor((now - start) / (7 * 864e5)) + 1;
  const weeks = Math.max(1, Math.round(spanOf(running) / (7 * 864e5)));
  return (
    <span className="badge" title="the phase today falls into">
      {running.summary} · week {Math.min(week, weeks)} of {weeks}
    </span>
  );
}

function spanOf(o: Occurrence) {
  return new Date(o.end).getTime() - new Date(o.start).getTime();
}

// ---------------------------------------------------------- deadline panel

/** Everything owed, nearest first — because that is the question a deadline answers. */
function DeadlinePanel({
  deadlines,
  onPick,
  onToggleDone,
}: {
  deadlines: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onToggleDone: (e: Occurrence) => void;
}) {
  if (!deadlines.length) return null;
  const open = deadlines.filter((d) => !d.done).sort((a, b) => a.start.localeCompare(b.start));
  const done = deadlines.filter((d) => d.done).sort((a, b) => b.start.localeCompare(a.start));
  const overdue = open.filter((d) => new Date(d.start).getTime() < Date.now());

  return (
    <aside className="cal-side">
      <h3>
        Upcoming deadlines
        {overdue.length ? <span className="badge bad">{overdue.length} overdue</span> : null}
      </h3>
      {open.map((d) => (
        <DeadlineRow key={keyOf(d)} d={d} onPick={onPick} onToggleDone={onToggleDone} />
      ))}
      {!open.length ? <div className="sub">Nothing owed in this range.</div> : null}
      {done.slice(0, 5).map((d) => (
        <DeadlineRow key={keyOf(d)} d={d} onPick={onPick} onToggleDone={onToggleDone} />
      ))}
    </aside>
  );
}

function DeadlineRow({
  d,
  onPick,
  onToggleDone,
}: {
  d: Occurrence;
  onPick: (e: Partial<Occurrence>) => void;
  onToggleDone: (e: Occurrence) => void;
}) {
  const late = !d.done && new Date(d.start).getTime() < Date.now();
  return (
    <div className={["cal-deadline-row", d.done ? "done" : "", late ? "late" : ""].join(" ")}>
      <button
        className="btn icon small"
        title={d.done ? "put it back" : "tick it off"}
        disabled={d.readOnly}
        onClick={() => onToggleDone(d)}
      >
        <Icon name={d.done ? "check" : "circle"} size={14} />
      </button>
      <button className="grow" onClick={() => onPick(d)}>
        <span className="summary">{d.summary}</span>
        <span className="meta">{distance(new Date(d.start))}</span>
      </button>
      {d.priority && d.priority <= 4 ? (
        <span className={d.priority <= 2 ? "badge bad" : "badge warn"}>
          {d.priority <= 2 ? "critical" : "important"}
        </span>
      ) : null}
    </div>
  );
}

// ------------------------------------------------------------------- views

function MonthView({
  anchor,
  events,
  onPick,
  onMove,
  onNew,
}: {
  anchor: Date;
  events: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onMove: (e: Occurrence, start: Date) => void;
  onNew: (day: Date) => void;
}) {
  const first = startOfWeek(new Date(anchor.getFullYear(), anchor.getMonth(), 1));
  const days = Array.from({ length: 42 }, (_, i) => addDays(first, i));
  const today = isoDate(new Date());

  return (
    <div className="cal-grid">
      {["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"].map((d) => (
        <div key={d} className="cal-dow">
          {d}
        </div>
      ))}
      {days.map((day) => {
        const key = isoDate(day);
        const dayEvents = events.filter((e) => overlapsDay(e, day));
        const marks = dayEvents.filter((e) => e.kind === "deadline");
        const blocks = dayEvents.filter((e) => e.kind !== "deadline");
        const clashes = overlapping(blocks);
        return (
          <div
            key={key}
            className={[
              "cal-day",
              day.getMonth() !== anchor.getMonth() ? "other" : "",
              key === today ? "today" : "",
            ].join(" ")}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              const payload = e.dataTransfer.getData("text/event");
              if (!payload) return;
              const event = JSON.parse(payload) as Occurrence;
              const from = new Date(event.start);
              const target = new Date(day);
              target.setHours(from.getHours(), from.getMinutes(), 0, 0);
              onMove(event, target);
            }}
            onClick={() => onNew(day)}
          >
            <span className="num">{day.getDate()}</span>
            {marks.map((e) => (
              <DeadlineMark key={keyOf(e) + key} e={e} onPick={onPick} />
            ))}
            {blocks.slice(0, 4).map((e) => (
              <div
                key={keyOf(e) + key}
                className={["cal-event", clashes.has(keyOf(e)) ? "clash" : ""].join(" ")}
                style={{ ["--ev-color" as string]: colorVar(e.color) }}
                draggable={!e.readOnly}
                onDragStart={(ev) => ev.dataTransfer.setData("text/event", JSON.stringify(e))}
                onClick={(ev) => {
                  ev.stopPropagation();
                  onPick(e);
                }}
                title={
                  `${e.summary}${e.location ? " · " + e.location : ""}` +
                  (clashes.has(keyOf(e)) ? " · overlaps another entry" : "")
                }
              >
                {e.allDay ? "" : shortTime(new Date(e.start)) + " "}
                {e.summary}
              </div>
            ))}
            {blocks.length > 4 ? <div className="sub">+{blocks.length - 4} more</div> : null}
          </div>
        );
      })}
    </div>
  );
}

/** A deadline is a flag on the edge of the day, not a block from 23:00 to 23:59. */
function DeadlineMark({ e, onPick }: { e: Occurrence; onPick: (e: Partial<Occurrence>) => void }) {
  const late = !e.done && new Date(e.start).getTime() < Date.now();
  return (
    <div
      className={["cal-mark", e.done ? "done" : "", late ? "late" : ""].join(" ")}
      style={{ ["--ev-color" as string]: colorVar(e.color) }}
      onClick={(ev) => {
        ev.stopPropagation();
        onPick(e);
      }}
      title={`${e.summary} · ${distance(new Date(e.start))}`}
    >
      <Icon name={e.done ? "check" : "alert"} size={11} />
      <span>{e.allDay ? "" : shortTime(new Date(e.start)) + " "}{e.summary}</span>
    </div>
  );
}

function TimeView({
  anchor,
  days,
  events,
  onPick,
  onMove,
  onNew,
  showGaps,
}: {
  anchor: Date;
  days: number;
  events: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onMove: (e: Occurrence, start: Date) => void;
  onNew: (day: Date, hour: number) => void;
  showGaps?: boolean;
}) {
  const first = days === 1 ? startOfDay(anchor) : startOfWeek(anchor);
  const columns = Array.from({ length: days }, (_, i) => addDays(first, i));

  const timed = events.filter((e) => !e.allDay && e.kind !== "deadline");
  const strip = events.filter((e) => e.allDay || e.kind === "deadline");

  // The visible range covers the working day and widens for whatever falls
  // outside it, instead of hiding a 06:30 train behind a scrollbar.
  const starts = timed.map((e) => new Date(e.start).getHours());
  const ends = timed.map((e) => new Date(e.end).getHours() + 1);
  const fromHour = Math.min(6, ...(starts.length ? starts : [6]));
  const toHour = Math.max(22, ...(ends.length ? ends : [22]));
  const hours = Array.from({ length: Math.max(1, toHour - fromHour) }, (_, i) => fromHour + i);

  const layout = useMemo(() => columnsFor(timed), [timed]);
  const clashes = overlapping(timed);

  return (
    <div className="cal-week" style={{ gridTemplateColumns: `54px repeat(${days}, minmax(0, 1fr))` }}>
      <div className="cal-hour" />
      {columns.map((d) => (
        <div key={isoDate(d)} className="cal-dow">
          {d.toLocaleDateString(undefined, { weekday: "short", day: "numeric" })}
        </div>
      ))}

      <div className="cal-hour strip">all day</div>
      {columns.map((day) => (
        <div key={"strip" + isoDate(day)} className="cal-strip">
          {strip
            .filter((e) => overlapsDay(e, day))
            .map((e) =>
              e.kind === "deadline" ? (
                <DeadlineMark key={keyOf(e) + isoDate(day)} e={e} onPick={onPick} />
              ) : (
                <div
                  key={keyOf(e) + isoDate(day)}
                  className="cal-event"
                  style={{ ["--ev-color" as string]: colorVar(e.color) }}
                  onClick={() => onPick(e)}
                  title={e.summary}
                >
                  {e.summary}
                </div>
              ),
            )}
        </div>
      ))}

      {hours.map((hour) => (
        <div key={hour} style={{ display: "contents" }}>
          <div className="cal-hour">{String(hour).padStart(2, "0")}:00</div>
          {columns.map((day) => {
            const slotStart = new Date(day);
            slotStart.setHours(hour, 0, 0, 0);
            const slotEvents = timed.filter(
              (e) => new Date(e.start).getHours() === hour && sameDay(new Date(e.start), day),
            );
            return (
              <div
                key={isoDate(day) + hour}
                className="cal-slot"
                onDragOver={(e) => e.preventDefault()}
                onDrop={(e) => {
                  e.preventDefault();
                  const payload = e.dataTransfer.getData("text/event");
                  if (!payload) return;
                  onMove(JSON.parse(payload) as Occurrence, slotStart);
                }}
                onClick={() => onNew(day, hour)}
              >
                {slotEvents.map((e) => {
                  const start = new Date(e.start);
                  const end = new Date(e.end);
                  const minutes = Math.max(20, (end.getTime() - start.getTime()) / 60000);
                  const place = layout.get(keyOf(e)) ?? { col: 0, of: 1 };
                  return (
                    <div
                      key={keyOf(e)}
                      className={["cal-abs", clashes.has(keyOf(e)) ? "clash" : ""].join(" ")}
                      draggable={!e.readOnly}
                      onDragStart={(ev) => ev.dataTransfer.setData("text/event", JSON.stringify(e))}
                      style={{
                        ["--ev-color" as string]: colorVar(e.color),
                        top: `${(start.getMinutes() / 60) * 44}px`,
                        height: `${(minutes / 60) * 44}px`,
                        left: `calc(${(place.col / place.of) * 100}% + 2px)`,
                        width: `calc(${100 / place.of}% - 4px)`,
                      }}
                      onClick={(ev) => {
                        ev.stopPropagation();
                        onPick(e);
                      }}
                      title={clashes.has(keyOf(e)) ? `${e.summary} · overlaps another entry` : e.summary}
                    >
                      <strong>{e.summary}</strong>
                      <div style={{ fontSize: 10.5, opacity: 0.85 }}>
                        {shortTime(start)}–{shortTime(end)}
                        {e.location ? ` · ${e.location}` : ""}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
      ))}

      {showGaps ? <GapRow events={timed} /> : null}
    </div>
  );
}

/** "2h 15m free" between two slots — because the real question is often when there is time. */
function GapRow({ events }: { events: Occurrence[] }) {
  const sorted = [...events].sort((a, b) => a.start.localeCompare(b.start));
  const gaps: { from: Date; to: Date }[] = [];
  let cursor: Date | null = null;
  for (const e of sorted) {
    const start = new Date(e.start);
    const end = new Date(e.end);
    if (cursor && start.getTime() - cursor.getTime() >= 15 * 60000) gaps.push({ from: cursor, to: start });
    if (!cursor || end > cursor) cursor = end;
  }
  if (!gaps.length) return null;
  return (
    <>
      <div className="cal-hour strip">gaps</div>
      <div className="cal-strip" style={{ gridColumn: "2 / -1" }}>
        {gaps.map((g, i) => (
          <span key={i} className="badge">
            {shortTime(g.from)}–{shortTime(g.to)} · {lengthOf(g.to.getTime() - g.from.getTime())}
          </span>
        ))}
      </div>
    </>
  );
}

function ListView({
  events,
  onPick,
  onToggleDone,
}: {
  events: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onToggleDone: (e: Occurrence) => void;
}) {
  if (!events.length) return <div className="empty">Nothing in this range.</div>;
  const byDay = new Map<string, Occurrence[]>();
  for (const e of events) {
    const key = isoDate(new Date(e.start));
    byDay.set(key, [...(byDay.get(key) ?? []), e]);
  }
  return (
    <div className="list">
      {[...byDay.entries()].map(([day, list]) => (
        <div key={day}>
          <div className="list-head">
            {new Date(day).toLocaleDateString(undefined, {
              weekday: "long",
              day: "numeric",
              month: "long",
            })}
          </div>
          {list.map((e) => (
            <div
              key={keyOf(e)}
              className={["list-row", e.kind === "deadline" ? "deadline" : "", e.done ? "done" : ""].join(" ")}
              style={{ cursor: "pointer" }}
              onClick={() => onPick(e)}
            >
              <span className="dot-status" style={{ background: colorVar(e.color) }} />
              <span className="meta" style={{ minWidth: 96 }}>
                {e.allDay ? "all day" : shortTime(new Date(e.start))}
              </span>
              <span className="grow">
                {e.summary}
                {e.location ? <span className="meta"> · {e.location}</span> : null}
              </span>
              {e.kind !== "slot" && e.kind !== "all-day" ? <span className="badge">{e.kind}</span> : null}
              {e.kind === "deadline" ? (
                <button
                  className="btn small"
                  disabled={e.readOnly}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    onToggleDone(e);
                  }}
                >
                  {e.done ? "done" : distance(new Date(e.start))}
                </button>
              ) : null}
              {e.repeats ? <span className="badge">repeats</span> : null}
              {e.readOnly ? <span className="badge warn">from a subscription</span> : null}
              {e.projectTitle ? <span className="badge">{e.projectTitle}</span> : null}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// ------------------------------------------------------------------ dialog

/**
 * Bring a calendar in from a file.
 *
 * Two shapes are taken: an .ics from anywhere, and the .json the old home
 * server wrote when it exported a calendar — that one is what somebody has
 * lying around after moving here, and it would be a poor answer to say convert
 * it yourself first.
 */
function ImportButton({
  project,
  onDone,
  onFailed,
}: {
  project: string;
  onDone: () => void;
  onFailed: (error: Error) => void;
}) {
  const pick = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState("");
  return (
    <>
      <input
        ref={pick}
        type="file"
        accept=".ics,.json,text/calendar,application/json"
        style={{ display: "none" }}
        onChange={async (e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (!file) return;
          setBusy(true);
          try {
            const answer = await api<{ imported: number }>(`/api/projects/${project}/calendar/import`, {
              raw: await file.arrayBuffer(),
              headers: { "Content-Type": file.name.endsWith(".json") ? "application/json" : "text/calendar" },
            });
            setSaid(`${answer.imported} brought in`);
            onDone();
          } catch (err) {
            onFailed(err as Error);
          } finally {
            setBusy(false);
          }
        }}
      />
      <button className="btn small" disabled={busy} onClick={() => pick.current?.click()} title="An .ics file, or a calendar exported by the old home server">
        <Icon name="upload" size={14} /> Import
      </button>
      {said ? <span className="meta">{said}</span> : null}
    </>
  );
}

function NewEntryButton({ onPick }: { onPick: (k: Kind) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ position: "relative" }}>
      <button className="btn small primary" onClick={() => setOpen((o) => !o)}>
        <Icon name="plus" size={14} /> New
      </button>
      {open ? (
        <>
          <div className="menu-backdrop" onClick={() => setOpen(false)} />
          <div className="menu">
            {KINDS.map((k) => (
              <button
                key={k.key}
                onClick={() => {
                  setOpen(false);
                  onPick(k.key);
                }}
              >
                <strong>{k.title}</strong>
                <span className="meta">{k.hint}</span>
              </button>
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}

function EventDialog({
  initial,
  sources,
  phases,
  onClose,
  onSaved,
}: {
  initial: Partial<Occurrence>;
  sources: { id: string; title: string; readOnly?: boolean }[];
  phases: Occurrence[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const isNew = !initial.uid;
  const [form, setForm] = useState({
    projectId: initial.projectId ?? sources[0]?.id ?? "",
    kind: (initial.kind ?? lastUsedKind()) as Kind,
    summary: initial.summary ?? "",
    description: initial.description ?? "",
    location: initial.location ?? "",
    start: toLocalInput(initial.start),
    end: toLocalInput(initial.end),
    allDay: initial.allDay ?? false,
    rrule: initial.rrule ?? "",
    alarms: (initial.alarms ?? []).join(","),
    priority: String(initial.priority ?? 0),
    done: initial.done ?? false,
    categories: (initial.categories ?? []).join(", "),
    relatedTo: initial.relatedTo ?? "",
    link: initial.link ?? "",
    person: initial.person ?? "",
    scope: "all" as "all" | "single",
  });
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [moveTarget, setMoveTarget] = useState("");

  const kind = form.kind;
  const isPoint = kind === "deadline" || kind === "milestone";
  const wholeDays = kind === "all-day" || kind === "phase" || kind === "milestone";

  // The kind decides which fields exist: a slot wants start and end, a
  // deadline only a due time, a phase two dates.
  const setKind = (next: Kind) => {
    setForm((f) => ({
      ...f,
      kind: next,
      allDay: next === "all-day" || next === "phase" || next === "milestone",
      alarms: next === "deadline" && !f.alarms ? "1440,60" : f.alarms,
    }));
  };

  const body = () => {
    const start = new Date(form.start);
    const end = isPoint ? start : new Date(form.end);
    return {
      summary: form.summary,
      description: form.description,
      location: form.location,
      start: start.toISOString(),
      end: end.toISOString(),
      allDay: wholeDays,
      rrule: form.rrule,
      kind: form.kind,
      priority: parseInt(form.priority, 10) || 0,
      done: kind === "deadline" ? form.done : undefined,
      categories: form.categories
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
      relatedTo: form.relatedTo,
      link: form.link,
      person: form.person,
      alarms: form.alarms
        .split(",")
        .map((s) => parseInt(s.trim(), 10))
        .filter((n) => !Number.isNaN(n)),
      scope: form.scope,
      recurrenceId: initial.recurrenceId,
    };
  };

  return (
    <Modal
      title={isNew ? `New ${KINDS.find((k) => k.key === kind)?.title.toLowerCase()}` : initial.summary || "Entry"}
      onClose={onClose}
      footer={
        <>
          {!isNew && !initial.readOnly ? (
            <button
              className="btn"
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  // A duplicate is a new entry with the same content — no uid,
                  // so the server gives it one of its own.
                  await api(`/api/projects/${form.projectId}/calendar/events`, {
                    body: { ...body(), summary: `${form.summary} (copy)` },
                  });
                  onSaved();
                } catch (err) {
                  setError(err as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Icon name="copy" size={15} /> Duplicate
            </button>
          ) : null}
          {!isNew && !initial.readOnly ? (
            <button
              className="btn danger"
              onClick={async () => {
                setBusy(true);
                try {
                  const scope = form.scope === "single" ? `?scope=single&recurrenceId=${initial.recurrenceId}` : "";
                  await api(
                    `/api/projects/${initial.projectId}/calendar/events/${encodeURIComponent(initial.uid!)}${scope}`,
                    { method: "DELETE" },
                  );
                  onSaved();
                } catch (err) {
                  setError(err as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Icon name="trash" size={15} /> Delete
            </button>
          ) : null}
          <div style={{ flex: 1 }} />
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          {!initial.readOnly ? (
            <button
              className="btn primary"
              disabled={busy || !form.summary.trim()}
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  if (isNew) {
                    await api(`/api/projects/${form.projectId}/calendar/events`, { body: body() });
                    rememberProject(form.projectId);
                    rememberKind(form.kind);
                  } else {
                    await api(
                      `/api/projects/${initial.projectId}/calendar/events/${encodeURIComponent(initial.uid!)}`,
                      { method: "PATCH", body: body() },
                    );
                    if (moveTarget && moveTarget !== initial.projectId) {
                      await api(
                        `/api/projects/${initial.projectId}/calendar/events/${encodeURIComponent(initial.uid!)}/move`,
                        { method: "POST", body: { targetProject: moveTarget } },
                      );
                    }
                  }
                  onSaved();
                } catch (err) {
                  setError(err as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              Save
            </button>
          ) : null}
        </>
      }
    >
      <ErrorBox error={error} />
      {initial.readOnly ? (
        <div className="warning">From a subscription — read-only, overwritten on the next run.</div>
      ) : null}

      <Field label="Kind" hint={KINDS.find((k) => k.key === kind)?.hint}>
        <select value={kind} onChange={(e) => setKind(e.target.value as Kind)}>
          {KINDS.map((k) => (
            <option key={k.key} value={k.key}>
              {k.title}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Title">
        <input value={form.summary} onChange={(e) => setForm({ ...form, summary: e.target.value })} autoFocus />
      </Field>

      {isNew && sources.length > 1 ? (
        <Field label="Calendar">
          <select value={form.projectId} onChange={(e) => setForm({ ...form, projectId: e.target.value })}>
            {sources
              .filter((s) => !s.readOnly)
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
          </select>
        </Field>
      ) : null}

      {isPoint ? (
        <Field label={kind === "deadline" ? "Due" : "On"}>
          <input
            type={wholeDays ? "date" : "datetime-local"}
            value={wholeDays ? form.start.slice(0, 10) : form.start}
            onChange={(e) =>
              setForm({ ...form, start: wholeDays ? `${e.target.value}T00:00` : e.target.value })
            }
          />
        </Field>
      ) : (
        <div className="row">
          <Field label="Start">
            <input
              type={wholeDays ? "date" : "datetime-local"}
              value={wholeDays ? form.start.slice(0, 10) : form.start}
              onChange={(e) =>
                setForm({ ...form, start: wholeDays ? `${e.target.value}T00:00` : e.target.value })
              }
            />
          </Field>
          <Field label="End">
            <input
              type={wholeDays ? "date" : "datetime-local"}
              value={wholeDays ? form.end.slice(0, 10) : form.end}
              onChange={(e) => setForm({ ...form, end: wholeDays ? `${e.target.value}T00:00` : e.target.value })}
            />
          </Field>
        </div>
      )}

      {kind === "deadline" ? (
        <>
          <div className="row">
            <Field label="Importance">
              <select value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })}>
                <option value="0">normal</option>
                <option value="4">important</option>
                <option value="1">critical</option>
              </select>
            </Field>
            <Field label="State">
              <select
                value={form.done ? "done" : "open"}
                onChange={(e) => setForm({ ...form, done: e.target.value === "done" })}
              >
                <option value="open">open</option>
                <option value="done">done</option>
              </select>
            </Field>
          </div>

        </>
      ) : null}

      {kind === "slot" ? (
        <Field label="Room">
          <input value={form.location} onChange={(e) => setForm({ ...form, location: e.target.value })} />
        </Field>
      ) : null}

      <Field label="Description">
        <textarea
          style={{ minHeight: 70 }}
          value={form.description}
          onChange={(e) => setForm({ ...form, description: e.target.value })}
        />
      </Field>

      {kind !== "phase" ? (
        <Field label="Repeat" hint="An RFC 5545 rule, e.g. FREQ=WEEKLY;BYDAY=MO — empty means once.">
          <select value={form.rrule} onChange={(e) => setForm({ ...form, rrule: e.target.value })}>
            <option value="">Once</option>
            <option value="FREQ=DAILY">Daily</option>
            <option value="FREQ=WEEKLY">Weekly</option>
            <option value="FREQ=WEEKLY;INTERVAL=2">Every two weeks</option>
            <option value="FREQ=MONTHLY">Monthly</option>
            <option value="FREQ=YEARLY">Yearly</option>
            {form.rrule &&
            !["FREQ=DAILY", "FREQ=WEEKLY", "FREQ=WEEKLY;INTERVAL=2", "FREQ=MONTHLY", "FREQ=YEARLY"].includes(
              form.rrule,
            ) ? (
              <option value={form.rrule}>{form.rrule}</option>
            ) : null}
          </select>
        </Field>
      ) : null}

      {initial.repeats ? (
        <Field label="This change applies to">
          <select value={form.scope} onChange={(e) => setForm({ ...form, scope: e.target.value as "all" | "single" })}>
            <option value="all">the whole series</option>
            <option value="single">only this appearance</option>
          </select>
        </Field>
      ) : null}

      {kind !== "phase" ? (
        <Field
          label="Reminders"
          hint="Minutes before, comma separated."
        >
          <input value={form.alarms} onChange={(e) => setForm({ ...form, alarms: e.target.value })} placeholder="15" />
        </Field>
      ) : null}

      {phases.length && kind !== "phase" ? (
        <Field label="Belongs to" hint="A phase.">
          <select value={form.relatedTo} onChange={(e) => setForm({ ...form, relatedTo: e.target.value })}>
            <option value="">nothing in particular</option>
            {phases.map((p) => (
              <option key={p.uid} value={p.uid}>
                {p.summary}
              </option>
            ))}
          </select>
        </Field>
      ) : null}

      <Field label="Tags" hint="Comma separated.">
        <input value={form.categories} onChange={(e) => setForm({ ...form, categories: e.target.value })} />
      </Field>

      <Field
        label="Link into the project"
        hint="A folder or file in this project."
      >
        <div style={{ display: "flex", gap: 8 }}>
          <input
            value={form.link}
            onChange={(e) => setForm({ ...form, link: e.target.value })}
            placeholder="lectures/analysis"
          />
          {form.link ? (
            <a className="btn small" href={filesHref(form.projectId, form.link)}>
              <Icon name="folder" size={14} /> Open
            </a>
          ) : null}
        </div>
      </Field>

      {kind === "slot" ? (
        <Field label="Person" hint="Lecturer — filterable.">
          <input value={form.person} onChange={(e) => setForm({ ...form, person: e.target.value })} />
        </Field>
      ) : null}

      {!isNew && sources.length > 1 ? (
        <Field label="Move to another calendar">
          <select value={moveTarget} onChange={(e) => setMoveTarget(e.target.value)}>
            <option value="">stay here</option>
            {sources
              .filter((s) => s.id !== initial.projectId && !s.readOnly)
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
          </select>
        </Field>
      ) : null}
    </Modal>
  );
}

// ------------------------------------------------------------------ helpers

/**
 * Creating an entry asks which project it goes into, with the last used one
 * preselected — remembered locally, per browser. The same for the kind.
 */
function lastUsedProject(available: string[]): string | undefined {
  try {
    const id = localStorage.getItem("calendar.lastProject");
    return id && available.includes(id) ? id : undefined;
  } catch {
    return undefined;
  }
}

function rememberProject(id: string) {
  try {
    localStorage.setItem("calendar.lastProject", id);
  } catch {
    /* private mode */
  }
}

/**
 * Where a link points. A file opens the folder it lies in, because that is
 * what the files tab shows.
 */
function filesHref(projectId: string, link: string) {
  const path = link.replace(/^project:/, "").replace(/^\/+/, "");
  const last = path.split("/").pop() ?? "";
  const folder = last.includes(".") ? path.split("/").slice(0, -1).join("/") : path;
  return `/p/${projectId}?tab=files&path=${encodeURIComponent(folder)}`;
}

function lastUsedKind(): Kind {
  try {
    const k = localStorage.getItem("calendar.lastKind") as Kind | null;
    return k && KINDS.some((x) => x.key === k) ? k : "slot";
  } catch {
    return "slot";
  }
}

function rememberKind(k: Kind) {
  try {
    localStorage.setItem("calendar.lastKind", k);
  } catch {
    /* private mode */
  }
}

/**
 * Which entries share a time with another one. With a timetable that is almost
 * always a mistake worth seeing, so it gets a marker rather than silence.
 */
function overlapping(events: Occurrence[]): Set<string> {
  const out = new Set<string>();
  for (let i = 0; i < events.length; i++) {
    for (let j = i + 1; j < events.length; j++) {
      const a = events[i];
      const b = events[j];
      if (a.allDay || b.allDay) continue;
      if (new Date(a.start) < new Date(b.end) && new Date(b.start) < new Date(a.end)) {
        out.add(keyOf(a));
        out.add(keyOf(b));
      }
    }
  }
  return out;
}

/** Overlapping slots sit side by side, not on top of each other. */
function columnsFor(events: Occurrence[]): Map<string, { col: number; of: number }> {
  const out = new Map<string, { col: number; of: number }>();
  const byDay = new Map<string, Occurrence[]>();
  for (const e of events) {
    const key = isoDate(new Date(e.start));
    byDay.set(key, [...(byDay.get(key) ?? []), e]);
  }
  for (const list of byDay.values()) {
    const sorted = [...list].sort((a, b) => a.start.localeCompare(b.start));
    // One cluster is a run of entries that touch each other; every member of a
    // cluster is divided by the same number, so the columns line up.
    let cluster: Occurrence[] = [];
    let clusterEnd = 0;
    const flush = () => {
      const cols: number[] = [];
      for (const e of cluster) {
        const start = new Date(e.start).getTime();
        let col = cols.findIndex((end) => end <= start);
        if (col === -1) {
          col = cols.length;
          cols.push(0);
        }
        cols[col] = new Date(e.end).getTime();
        out.set(keyOf(e), { col, of: 1 });
      }
      for (const e of cluster) {
        const place = out.get(keyOf(e))!;
        out.set(keyOf(e), { col: place.col, of: Math.max(1, cols.length) });
      }
      cluster = [];
      clusterEnd = 0;
    };
    for (const e of sorted) {
      const start = new Date(e.start).getTime();
      if (cluster.length && start >= clusterEnd) flush();
      cluster.push(e);
      clusterEnd = Math.max(clusterEnd, new Date(e.end).getTime());
    }
    flush();
  }
  return out;
}

/** "in 3 days", "tomorrow 23:59", "2 days overdue" — the distance in plain words. */
function distance(target: Date): string {
  const ms = target.getTime() - Date.now();
  const days = Math.round(ms / 864e5);
  const hours = Math.round(ms / 36e5);
  if (ms < 0) {
    const late = -ms;
    if (late < 36e5) return "overdue";
    if (late < 864e5) return `${Math.round(late / 36e5)}h overdue`;
    return `${Math.round(late / 864e5)} days overdue`;
  }
  if (hours < 1) return "in under an hour";
  if (hours < 24 && target.getDate() === new Date().getDate()) return `today ${shortTime(target)}`;
  if (days <= 1) return `tomorrow ${shortTime(target)}`;
  if (days < 14) return `in ${days} days`;
  return target.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

function lengthOf(ms: number) {
  const h = Math.floor(ms / 36e5);
  const m = Math.round((ms % 36e5) / 60000);
  return h ? `${h}h ${String(m).padStart(2, "0")}m` : `${m}m`;
}

function keyOf(e: Occurrence) {
  return `${e.projectId}:${e.uid}:${e.recurrenceId ?? e.start}`;
}

function isoDate(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** The week starts on Monday — on the web and in the app. */
function startOfWeek(d: Date) {
  const copy = startOfDay(d);
  const day = (copy.getDay() + 6) % 7;
  copy.setDate(copy.getDate() - day);
  return copy;
}

function startOfDay(d: Date) {
  const copy = new Date(d);
  copy.setHours(0, 0, 0, 0);
  return copy;
}

function addDays(d: Date, n: number) {
  const copy = new Date(d);
  copy.setDate(copy.getDate() + n);
  return copy;
}

function atHour(d: Date, hour: number) {
  const copy = startOfDay(d);
  copy.setHours(hour);
  return copy;
}

function atTime(d: Date, hour: number, minute: number) {
  const copy = startOfDay(d);
  copy.setHours(hour, minute);
  return copy;
}

function sameDay(a: Date, b: Date) {
  return isoDate(a) === isoDate(b);
}

function overlapsDay(e: Occurrence, day: Date) {
  const start = new Date(e.start);
  const end = new Date(e.end);
  const from = startOfDay(day);
  const to = addDays(from, 1);
  // A point in time has no length, so "overlap" would never be true for it.
  if (start.getTime() === end.getTime()) return start >= from && start < to;
  return start < to && end > from;
}

function shortTime(d: Date) {
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
}

function toLocalInput(iso?: string) {
  const d = iso ? new Date(iso) : new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function rangeFor(view: View, anchor: Date) {
  if (view === "day") return { from: startOfDay(anchor), to: addDays(startOfDay(anchor), 1) };
  if (view === "week") return { from: startOfWeek(anchor), to: addDays(startOfWeek(anchor), 7) };
  if (view === "list") return { from: startOfDay(anchor), to: addDays(startOfDay(anchor), 60) };
  const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
  return { from: startOfWeek(first), to: addDays(startOfWeek(first), 42) };
}

function step(view: View, anchor: Date, direction: number) {
  if (view === "day") return addDays(anchor, direction);
  if (view === "week") return addDays(anchor, 7 * direction);
  if (view === "list") return addDays(anchor, 30 * direction);
  return new Date(anchor.getFullYear(), anchor.getMonth() + direction, 1);
}

function titleFor(view: View, anchor: Date) {
  if (view === "day") return anchor.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
  if (view === "week") {
    const from = startOfWeek(anchor);
    const to = addDays(from, 6);
    return `${from.toLocaleDateString(undefined, { day: "numeric", month: "short" })} – ${to.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}`;
  }
  return anchor.toLocaleDateString(undefined, { month: "long", year: "numeric" });
}
