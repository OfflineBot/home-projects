import { useMemo, useState } from "react";
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
 */

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
}

type View = "month" | "week" | "day" | "list";

export default function CalendarView({ project }: { project: Project; reload: () => void }) {
  return (
    <CalendarGrid
      sources={[{ id: project.id, title: project.title, color: project.color, readOnly: project.readOnly }]}
      endpoint={(from, to) =>
        `/api/projects/${project.id}/calendar/events?from=${from}&to=${to}`
      }
      defaultProject={project.id}
      showSubscription={project.id}
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

  const setView = (next: View) => {
    const p = new URLSearchParams(params);
    p.set("view", next);
    setParams(p);
    // The last choice is remembered on the server too, so the next visit opens
    // the same way.
    if (showSubscription) {
      void api(`/api/projects/${showSubscription}/calendar/settings`, {
        method: "PUT",
        body: { lastView: next },
      }).catch(() => undefined);
    }
  };
  const setAnchor = (d: Date) => {
    const p = new URLSearchParams(params);
    p.set("at", isoDate(d));
    setParams(p);
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

  const events = (data?.events ?? [])
    .filter((e) => !hidden?.has(e.projectId))
    .map((e) => ({ ...e, ...(optimistic[keyOf(e)] ?? {}) }));

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

  const title = titleFor(view, anchor);

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
        <span className="title">{title}</span>
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
        <button
          className="btn small primary"
          onClick={() =>
            setEditing({
              projectId: lastUsedProject(sources.map((s) => s.id)) ?? defaultProject ?? sources[0]?.id,
              start: atHour(anchor, 9).toISOString(),
              end: atHour(anchor, 10).toISOString(),
              summary: "",
            })
          }
        >
          <Icon name="plus" size={14} /> Event
        </button>
      </div>

      {extraHeader}

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

      {view === "month" ? (
        <MonthView anchor={anchor} events={events} onPick={setEditing} onMove={moveTo} defaultProject={defaultProject} />
      ) : view === "list" ? (
        <ListView events={events} onPick={setEditing} />
      ) : (
        <TimeView
          anchor={anchor}
          days={view === "day" ? 1 : 7}
          events={events}
          onPick={setEditing}
          onMove={moveTo}
          defaultProject={defaultProject}
        />
      )}

      {editing ? (
        <EventDialog
          initial={editing}
          sources={sources}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      ) : null}

      {subscription ? (
        <Modal title="Subscribe to this calendar" onClose={() => setSubscription(null)}>
          <p style={{ marginTop: 0 }}>
            This address returns one complete <code className="mono">VCALENDAR</code> — Google Calendar,
            Thunderbird and iOS can be pointed straight at it, without an account on this server.
          </p>
          <input readOnly value={subscription} onFocus={(e) => e.currentTarget.select()} />
        </Modal>
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
  defaultProject,
}: {
  anchor: Date;
  events: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onMove: (e: Occurrence, start: Date) => void;
  defaultProject?: string;
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
            onClick={() =>
              onPick({
                projectId: defaultProject,
                start: atHour(day, 9).toISOString(),
                end: atHour(day, 10).toISOString(),
                summary: "",
              })
            }
          >
            <span className="num">{day.getDate()}</span>
            {dayEvents.slice(0, 4).map((e) => (
              <div
                key={keyOf(e) + key}
                className="cal-event"
                style={{ ["--ev-color" as string]: colorVar(e.color) }}
                draggable={!e.readOnly}
                onDragStart={(ev) => ev.dataTransfer.setData("text/event", JSON.stringify(e))}
                onClick={(ev) => {
                  ev.stopPropagation();
                  onPick(e);
                }}
                title={`${e.summary}${e.location ? " · " + e.location : ""}`}
              >
                {e.allDay ? "" : shortTime(new Date(e.start)) + " "}
                {e.summary}
              </div>
            ))}
            {dayEvents.length > 4 ? <div className="sub">+{dayEvents.length - 4} more</div> : null}
          </div>
        );
      })}
    </div>
  );
}

function TimeView({
  anchor,
  days,
  events,
  onPick,
  onMove,
  defaultProject,
}: {
  anchor: Date;
  days: number;
  events: Occurrence[];
  onPick: (e: Partial<Occurrence>) => void;
  onMove: (e: Occurrence, start: Date) => void;
  defaultProject?: string;
}) {
  const first = days === 1 ? startOfDay(anchor) : startOfWeek(anchor);
  const columns = Array.from({ length: days }, (_, i) => addDays(first, i));
  const hours = Array.from({ length: 24 }, (_, i) => i);

  return (
    <div className="cal-week" style={{ gridTemplateColumns: `54px repeat(${days}, minmax(0, 1fr))` }}>
      <div className="cal-hour" />
      {columns.map((d) => (
        <div key={isoDate(d)} className="cal-dow">
          {d.toLocaleDateString(undefined, { weekday: "short", day: "numeric" })}
        </div>
      ))}

      {hours.map((hour) => (
        <div key={hour} style={{ display: "contents" }}>
          <div className="cal-hour">{String(hour).padStart(2, "0")}:00</div>
          {columns.map((day) => {
            const slotStart = new Date(day);
            slotStart.setHours(hour, 0, 0, 0);
            const slotEvents = events.filter(
              (e) => !e.allDay && new Date(e.start).getHours() === hour && sameDay(new Date(e.start), day),
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
                onClick={() =>
                  onPick({
                    projectId: defaultProject,
                    start: slotStart.toISOString(),
                    end: new Date(slotStart.getTime() + 3600_000).toISOString(),
                    summary: "",
                  })
                }
              >
                {slotEvents.map((e, i) => {
                  const start = new Date(e.start);
                  const end = new Date(e.end);
                  const minutes = Math.max(20, (end.getTime() - start.getTime()) / 60000);
                  return (
                    <div
                      key={keyOf(e)}
                      className="cal-abs"
                      draggable={!e.readOnly}
                      onDragStart={(ev) => ev.dataTransfer.setData("text/event", JSON.stringify(e))}
                      style={{
                        ["--ev-color" as string]: colorVar(e.color),
                        top: `${(start.getMinutes() / 60) * 44}px`,
                        height: `${(minutes / 60) * 44}px`,
                        left: `${2 + i * 4}px`,
                      }}
                      onClick={(ev) => {
                        ev.stopPropagation();
                        onPick(e);
                      }}
                    >
                      <strong>{e.summary}</strong>
                      <div style={{ fontSize: 10.5, opacity: 0.85 }}>
                        {shortTime(start)}–{shortTime(end)}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}

function ListView({ events, onPick }: { events: Occurrence[]; onPick: (e: Partial<Occurrence>) => void }) {
  if (!events.length) return <div className="empty">Nothing in this range.</div>;
  return (
    <div className="list">
      {events.map((e) => (
        <div key={keyOf(e)} className="list-row" style={{ cursor: "pointer" }} onClick={() => onPick(e)}>
          <span className="dot-status" style={{ background: colorVar(e.color) }} />
          <span className="meta" style={{ minWidth: 140 }}>
            {new Date(e.start).toLocaleDateString(undefined, { weekday: "short", day: "2-digit", month: "short" })}
            {e.allDay ? " · all day" : ` · ${shortTime(new Date(e.start))}`}
          </span>
          <span className="grow">
            {e.summary}
            {e.location ? <span className="meta"> · {e.location}</span> : null}
          </span>
          {e.repeats ? <span className="badge">repeats</span> : null}
          {e.readOnly ? <span className="badge warn">from a subscription</span> : null}
          {e.projectTitle ? <span className="badge">{e.projectTitle}</span> : null}
        </div>
      ))}
    </div>
  );
}

// ------------------------------------------------------------------ dialog

function EventDialog({
  initial,
  sources,
  onClose,
  onSaved,
}: {
  initial: Partial<Occurrence>;
  sources: { id: string; title: string; readOnly?: boolean }[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const isNew = !initial.uid;
  const [form, setForm] = useState({
    projectId: initial.projectId ?? sources[0]?.id ?? "",
    summary: initial.summary ?? "",
    description: initial.description ?? "",
    location: initial.location ?? "",
    start: toLocalInput(initial.start),
    end: toLocalInput(initial.end),
    allDay: initial.allDay ?? false,
    rrule: initial.rrule ?? "",
    alarms: (initial.alarms ?? []).join(","),
    scope: "all" as "all" | "single",
  });
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [moveTarget, setMoveTarget] = useState("");

  const body = () => ({
    summary: form.summary,
    description: form.description,
    location: form.location,
    start: new Date(form.start).toISOString(),
    end: new Date(form.end).toISOString(),
    allDay: form.allDay,
    rrule: form.rrule,
    alarms: form.alarms
      .split(",")
      .map((s) => parseInt(s.trim(), 10))
      .filter((n) => !Number.isNaN(n)),
    scope: form.scope,
    recurrenceId: initial.recurrenceId,
  });

  return (
    <Modal
      title={isNew ? "New event" : initial.summary || "Event"}
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
                  // A duplicate is a new event with the same content — no uid,
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
        <div className="warning">
          This event comes from a subscription. It is read-only and gets overwritten on the next run.
        </div>
      ) : null}

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

      <label className="check">
        <input type="checkbox" checked={form.allDay} onChange={(e) => setForm({ ...form, allDay: e.target.checked })} />
        <span>All day</span>
      </label>

      <div className="row">
        <Field label="Start">
          <input
            type={form.allDay ? "date" : "datetime-local"}
            value={form.allDay ? form.start.slice(0, 10) : form.start}
            onChange={(e) =>
              setForm({ ...form, start: form.allDay ? `${e.target.value}T00:00` : e.target.value })
            }
          />
        </Field>
        <Field label="End">
          <input
            type={form.allDay ? "date" : "datetime-local"}
            value={form.allDay ? form.end.slice(0, 10) : form.end}
            onChange={(e) => setForm({ ...form, end: form.allDay ? `${e.target.value}T00:00` : e.target.value })}
          />
        </Field>
      </div>

      <Field label="Location">
        <input value={form.location} onChange={(e) => setForm({ ...form, location: e.target.value })} />
      </Field>
      <Field label="Description">
        <textarea
          style={{ minHeight: 70 }}
          value={form.description}
          onChange={(e) => setForm({ ...form, description: e.target.value })}
        />
      </Field>

      <Field label="Repeat" hint="An RFC 5545 rule, e.g. FREQ=WEEKLY;BYDAY=MO,WE — empty means once.">
        <select value={form.rrule} onChange={(e) => setForm({ ...form, rrule: e.target.value })}>
          <option value="">Once</option>
          <option value="FREQ=DAILY">Daily</option>
          <option value="FREQ=WEEKLY">Weekly</option>
          <option value="FREQ=WEEKLY;INTERVAL=2">Every two weeks</option>
          <option value="FREQ=MONTHLY">Monthly</option>
          <option value="FREQ=YEARLY">Yearly</option>
          {form.rrule && !["FREQ=DAILY", "FREQ=WEEKLY", "FREQ=WEEKLY;INTERVAL=2", "FREQ=MONTHLY", "FREQ=YEARLY"].includes(form.rrule) ? (
            <option value={form.rrule}>{form.rrule}</option>
          ) : null}
        </select>
      </Field>

      {initial.repeats ? (
        <Field label="This change applies to">
          <select value={form.scope} onChange={(e) => setForm({ ...form, scope: e.target.value as "all" | "single" })}>
            <option value="all">the whole series</option>
            <option value="single">only this appearance</option>
          </select>
        </Field>
      ) : null}

      <Field label="Reminders" hint="Minutes before the start, comma separated.">
        <input value={form.alarms} onChange={(e) => setForm({ ...form, alarms: e.target.value })} placeholder="15" />
      </Field>

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
 * Creating an event asks which project it goes into, with the last used one
 * preselected — remembered locally, per browser.
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

function sameDay(a: Date, b: Date) {
  return isoDate(a) === isoDate(b);
}

function overlapsDay(e: Occurrence, day: Date) {
  const start = new Date(e.start);
  const end = new Date(e.end);
  const from = startOfDay(day);
  const to = addDays(from, 1);
  return start < to && end > from;
}

function shortTime(d: Date) {
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
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
