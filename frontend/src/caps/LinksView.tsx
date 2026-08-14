import { useMemo, useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Spinner, formatDate, useAsk } from "../components/ui";
import { api, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/**
 * Addresses worth keeping: the thing to buy, the article to read, the page you
 * will never find again.
 *
 * One line to add one, a search, and tags. A project like this can be public,
 * and then it is a list two people keep together.
 */

interface Link {
  id: string;
  url: string;
  title: string;
  note?: string;
  tags?: string[];
  addedAt: string;
  done?: boolean;
}

export default function LinksView({ project }: { project: Project; reload: () => void }) {
  const ask = useAsk();
  const { data, error, loading, reload } = useQuery<{ links: Link[] }>(
    `/api/projects/${project.id}/links`,
  );
  const [draft, setDraft] = useState({ url: "", title: "", note: "", tags: "" });
  const [find, setFind] = useState("");
  const [tag, setTag] = useState("");
  const [showDone, setShowDone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<Error | null>(null);

  const links = data?.links ?? [];
  const tags = [...new Set(links.flatMap((l) => l.tags ?? []))].sort();

  const shown = useMemo(() => {
    const needle = find.trim().toLowerCase();
    return links.filter((l) => {
      if (!showDone && l.done) return false;
      if (tag && !(l.tags ?? []).includes(tag)) return false;
      if (!needle) return true;
      return (l.title + " " + l.url + " " + (l.note ?? "")).toLowerCase().includes(needle);
    });
  }, [links, find, tag, showDone]);

  const add = async () => {
    if (!draft.url.trim()) return;
    setBusy(true);
    setFailed(null);
    try {
      await api(`/api/projects/${project.id}/links`, {
        body: {
          url: draft.url,
          title: draft.title,
          note: draft.note,
          tags: draft.tags.split(",").map((t) => t.trim()).filter(Boolean),
        },
      });
      setDraft({ url: "", title: "", note: "", tags: "" });
      reload();
    } catch (err) {
      setFailed(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      {!project.readOnly ? (
        <div className="link-add">
          <div className="row">
            <Field label="Address" required>
              <input
                value={draft.url}
                placeholder="https://…"
                onChange={(e) => setDraft({ ...draft, url: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void add();
                }}
              />
            </Field>
            <Field label="Name" hint="Empty takes the site's own name." optional>
              <input value={draft.title} onChange={(e) => setDraft({ ...draft, title: e.target.value })} />
            </Field>
          </div>
          <div className="row" style={{ alignItems: "end" }}>
            <Field label="Why" optional>
              <input
                value={draft.note}
                placeholder="a line for later"
                onChange={(e) => setDraft({ ...draft, note: e.target.value })}
              />
            </Field>
            <Field label="Tags" hint="Comma separated." optional>
              <input
                value={draft.tags}
                placeholder="buy, kitchen"
                onChange={(e) => setDraft({ ...draft, tags: e.target.value })}
              />
            </Field>
            <button className="btn primary" disabled={busy || !draft.url.trim()} onClick={add}>
              <Icon name="plus" size={15} /> Keep it
            </button>
          </div>
        </div>
      ) : null}

      <div className="mail-bar" style={{ marginTop: 12 }}>
        <div className="mail-find">
          <Icon name="search" size={15} />
          <input value={find} onChange={(e) => setFind(e.target.value)} placeholder="Search" />
        </div>
        {tags.map((t) => (
          <button key={t} className={tag === t ? "chip on" : "chip"} onClick={() => setTag(tag === t ? "" : t)}>
            {t}
          </button>
        ))}
        <span className="grow" />
        <label className="check" style={{ margin: 0 }}>
          <input type="checkbox" checked={showDone} onChange={(e) => setShowDone(e.target.checked)} />
          <span className="meta">also the done ones</span>
        </label>
      </div>

      <ErrorBox error={failed ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data && links.length === 0 ? <Empty icon="link">Nothing kept yet.</Empty> : null}

      <div className="list">
        {shown.map((l) => (
          <div key={l.id} className={l.done ? "list-row done" : "list-row"}>
            <input
              type="checkbox"
              checked={Boolean(l.done)}
              title="Dealt with"
              disabled={project.readOnly}
              onChange={async (e) => {
                await api(`/api/projects/${project.id}/links/${l.id}`, {
                  method: "PATCH",
                  body: { done: e.target.checked },
                });
                reload();
              }}
            />
            <a className="grow" href={l.url} target="_blank" rel="noreferrer">
              <strong>{l.title}</strong>
              {l.note ? <span className="meta"> · {l.note}</span> : null}
              <div className="meta mono">{l.url}</div>
            </a>
            {(l.tags ?? []).map((t) => (
              <span key={t} className="chip tiny">
                {t}
              </span>
            ))}
            <span className="meta">{formatDate(l.addedAt, false)}</span>
            {!project.readOnly ? (
              <button
                className="btn ghost icon"
                aria-label={`Drop ${l.title}`}
                onClick={async () => {
                  const sure = await ask.confirm({
                    title: `Drop “${l.title}”?`,
                    confirmLabel: "Drop it",
                    danger: true,
                  });
                  if (!sure) return;
                  await api(`/api/projects/${project.id}/links/${l.id}`, { method: "DELETE" });
                  reload();
                }}
              >
                <Icon name="x" size={15} />
              </button>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}
