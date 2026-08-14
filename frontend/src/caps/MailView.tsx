import { useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner } from "../components/ui";
import { api, authedUrl, type Account, type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/**
 * The mailbox.
 *
 * A message is an .eml file in the project — that part does not change. What
 * changed is the reading: a list on the left and the message on the right, the
 * way every mail program has done it for thirty years, because this is opened
 * every day and a page of grey rows with =E4 in them is not readable.
 *
 * HTML mail is shown, in a sandboxed frame with no scripts and no access to
 * this page, with the colours of the theme forced over whatever the sender
 * decided. Inline images come from the message itself.
 */

interface Summary {
  path: string;
  folder: string;
  from: string;
  to: string;
  subject: string;
  date: string;
  size: number;
  hasAttachments?: boolean;
  category?: string;
  score?: number;
  fixed?: boolean;
}

interface Attachment {
  index: number;
  filename: string;
  type: string;
  size: number;
  contentId?: string;
  inline?: boolean;
}

export default function MailView({ project }: { project: Project; reload: () => void }) {
  const [params, setParams] = useSearchParams();
  const selected = params.get("mail");
  const { data, error, loading, reload: reloadList } = useQuery<{
    messages: Summary[];
    classifier?: { endpoint?: string };
    sortedBy?: string;
  }>(`/api/projects/${project.id}/mail/messages`);
  const [composing, setComposing] = useState(false);
  const [sorting, setSorting] = useState(false);
  const [sortError, setSortError] = useState<Error | null>(null);
  const [only, setOnly] = useState("");
  const [folder, setFolder] = useState("");
  const [find, setFind] = useState("");

  const messages = data?.messages ?? [];
  const categories = [...new Set(messages.map((m) => m.category).filter(Boolean))] as string[];
  const folders = [...new Set(messages.map((m) => m.folder).filter((f) => f && f !== "."))].sort();

  const shown = useMemo(() => {
    const needle = find.trim().toLowerCase();
    return messages.filter((m) => {
      if (only && m.category !== only) return false;
      if (folder && m.folder !== folder) return false;
      if (!needle) return true;
      return (m.subject + " " + m.from + " " + m.to).toLowerCase().includes(needle);
    });
  }, [messages, only, folder, find]);

  const classify = async () => {
    setSorting(true);
    setSortError(null);
    try {
      await api(`/api/projects/${project.id}/mail/classify`, { method: "POST" });
      reloadList();
    } catch (err) {
      setSortError(err as Error);
    } finally {
      setSorting(false);
    }
  };

  const open = (path: string | null) => {
    const p = new URLSearchParams(params);
    if (path) p.set("mail", path);
    else p.delete("mail");
    setParams(p);
  };

  return (
    <div className="mail">
      <div className="mail-bar">
        <div className="mail-find">
          <Icon name="search" size={15} />
          <input
            value={find}
            onChange={(e) => setFind(e.target.value)}
            placeholder="Search sender or subject"
            aria-label="Search the mail"
          />
          {find ? (
            <button className="btn ghost icon" aria-label="Clear" onClick={() => setFind("")}>
              <Icon name="x" size={14} />
            </button>
          ) : null}
        </div>
        <span className="meta">
          {shown.length === messages.length
            ? `${messages.length} messages`
            : `${shown.length} of ${messages.length}`}
        </span>
        {!project.readOnly ? (
          <>
            <button
              className="btn small"
              disabled={sorting}
              title={data?.classifier?.endpoint ? `Asks ${data.classifier.endpoint}` : "Sorts by a few plain rules"}
              onClick={classify}
            >
              <Icon name="zap" size={14} /> {sorting ? "sorting…" : "Sort"}
            </button>
            <button className="btn small primary" onClick={() => setComposing(true)}>
              <Icon name="plus" size={14} /> Write
            </button>
          </>
        ) : null}
      </div>

      {folders.length > 1 || categories.length ? (
        <div className="mail-chips">
          {folders.length > 1
            ? folders.map((f) => (
                <button
                  key={f}
                  className={folder === f ? "chip on" : "chip"}
                  onClick={() => setFolder(folder === f ? "" : f)}
                >
                  <Icon name="folder" size={12} /> {f}
                </button>
              ))
            : null}
          {categories.map((cat) => (
            <button
              key={cat}
              className={only === cat ? "chip on" : "chip"}
              onClick={() => setOnly(cat === only ? "" : cat)}
            >
              {cat}
            </button>
          ))}
        </div>
      ) : null}

      <ErrorBox error={sortError ?? error} onRetry={reloadList} />

      <div className={selected ? "mail-panes has-selection" : "mail-panes"}>
        <section className="mail-list">
          {loading && !data ? <Spinner /> : null}
          {data && messages.length === 0 ? <Empty icon="mail">Nothing here yet.</Empty> : null}
          {data && messages.length > 0 && shown.length === 0 ? (
            <p className="meta" style={{ padding: 14 }}>Nothing matches.</p>
          ) : null}
          {shown.map((m) => (
            <button
              key={m.path}
              className={m.path === selected ? "mail-row active" : "mail-row"}
              onClick={() => open(m.path)}
            >
              <span className="mail-avatar" aria-hidden="true">{initials(m.from)}</span>
              <span className="mail-row-main">
                <span className="mail-row-top">
                  <span className="mail-from">{senderName(m.from)}</span>
                  <span className="mail-when">{shortDate(m.date)}</span>
                </span>
                <span className="mail-subject">{m.subject || "(no subject)"}</span>
                <span className="mail-row-foot">
                  {m.category ? (
                    <span className="chip tiny" title={m.fixed ? "set by hand" : undefined}>
                      {m.category}
                    </span>
                  ) : null}
                  {m.folder && m.folder !== "." ? <span className="meta">{m.folder}</span> : null}
                  {m.hasAttachments ? <Icon name="link" size={12} /> : null}
                </span>
              </span>
            </button>
          ))}
        </section>

        <section className="mail-read">
          {selected ? (
            <Message key={selected} project={project} path={selected} onClose={() => open(null)} />
          ) : (
            <Empty icon="mail">Pick a message.</Empty>
          )}
        </section>
      </div>

      {composing ? <Compose project={project} onClose={() => setComposing(false)} onSent={reloadList} /> : null}
    </div>
  );
}

function Message({ project, path, onClose }: { project: Project; path: string; onClose: () => void }) {
  const { data, error, loading } = useQuery<{
    from: string;
    to: string;
    cc?: string;
    subject: string;
    date: string;
    text: string;
    html: string;
    attachments?: Attachment[];
    raw?: string;
    error?: string;
  }>(`/api/projects/${project.id}/mail/message?path=${encodeURIComponent(path)}`);

  const files = (data?.attachments ?? []).filter((a) => !a.inline || !a.contentId);
  const fileURL = (a: Attachment) =>
    authedUrl(`/api/projects/${project.id}/mail/attachment?path=${encodeURIComponent(path)}&i=${a.index}`);

  return (
    <article className="mail-message">
      <ErrorBox error={error} />
      {loading && !data ? <Spinner /> : null}
      {data ? (
        <>
          <header className="mail-head">
            <div className="mail-head-top">
              <h2>{data.subject || "(no subject)"}</h2>
              <button className="btn ghost icon" aria-label="Close" onClick={onClose}>
                <Icon name="x" size={16} />
              </button>
            </div>
            <div className="mail-head-who">
              <span className="mail-avatar big" aria-hidden="true">{initials(data.from)}</span>
              <div style={{ minWidth: 0 }}>
                <div className="mail-from">{data.from}</div>
                {data.to ? <div className="meta">To: {data.to}</div> : null}
                {data.cc ? <div className="meta">Cc: {data.cc}</div> : null}
              </div>
              <span className="meta mail-head-date">{longDate(data.date)}</span>
            </div>
          </header>

          {data.error ? <div className="warning">{data.error}</div> : null}

          <Body project={project} path={path} html={data.html} text={data.text}
                attachments={data.attachments ?? []} />

          {files.length ? (
            <div className="mail-files">
              {files.map((a) => (
                <a key={a.index} className="mail-file" href={fileURL(a)} download={a.filename}>
                  <Icon name="file" size={15} />
                  <span className="grow">{a.filename}</span>
                  <span className="meta">{size(a.size)}</span>
                </a>
              ))}
            </div>
          ) : null}

          <div className="mail-foot">
            <a
              className="btn small ghost"
              href={authedUrl(`/api/projects/${project.id}/files/download?path=${encodeURIComponent(path)}`)}
            >
              <Icon name="download" size={14} /> The .eml
            </a>
            <span className="meta mono">{path}</span>
          </div>
        </>
      ) : null}
    </article>
  );
}

/**
 * The message itself. HTML goes into a frame that may not run scripts and
 * cannot reach this page; the theme's colours are pushed in over whatever the
 * sender chose, because a white newsletter in a dark page is a torch.
 */
function Body({
  project,
  path,
  html,
  text,
  attachments,
}: {
  project: Project;
  path: string;
  html: string;
  text: string;
  attachments: Attachment[];
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(240);

  const document = useMemo(() => {
    if (!html) return "";
    const cleaned = clean(html);
    // cid: references point at the parts of this very message.
    const withImages = cleaned.replace(/(["'(])cid:([^"')\s]+)/gi, (whole, quote: string, id: string) => {
      const found = attachments.find((a) => (a.contentId ?? "").split("@")[0].toLowerCase() === id.split("@")[0].toLowerCase());
      if (!found) return whole;
      return (
        quote +
        authedUrl(`/api/projects/${project.id}/mail/attachment?path=${encodeURIComponent(path)}&i=${found.index}`)
      );
    });
    const style = readTheme();
    return `<!doctype html><html><head><meta charset="utf-8">
      <base target="_blank">
      <style>
        html,body{margin:0;padding:0 2px 12px;background:${style.bg}!important;color:${style.text}!important;
          font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;font-size:15px;line-height:1.55;
          word-wrap:break-word;overflow-x:hidden}
        body,p,div,span,td,th,li,h1,h2,h3,h4,h5,h6,strong,b,em,i,u,small,label,font{
          color:${style.text}!important;background:transparent!important;background-color:transparent!important}
        a,a *{color:${style.link}!important;text-decoration:underline;text-underline-offset:2px}
        img{max-width:100%!important;height:auto!important}
        table{max-width:100%!important}
        blockquote{border:none;margin:1em 0;padding:.4em .9em;color:${style.muted}!important;
          border-left:2px solid ${style.muted}}
        pre,code{background:${style.alt}!important;border-radius:6px;padding:.15em .35em;white-space:pre-wrap}
      </style></head><body>${withImages}</body></html>`;
  }, [html, attachments, project.id, path]);

  useEffect(() => {
    if (!document) return;
    const fit = () => {
      const body = frame.current?.contentDocument?.body;
      if (body) setHeight(Math.max(160, body.scrollHeight + 24));
    };
    window.addEventListener("resize", fit);
    return () => window.removeEventListener("resize", fit);
  }, [document]);

  if (document) {
    return (
      <iframe
        ref={frame}
        className="mail-body-frame"
        title="The message"
        sandbox="allow-popups allow-popups-to-escape-sandbox"
        srcDoc={document}
        style={{ height }}
        onLoad={() => {
          const body = frame.current?.contentDocument?.body;
          if (body) setHeight(Math.max(160, body.scrollHeight + 24));
        }}
      />
    );
  }
  if (text) return <pre className="mail-body-text">{text}</pre>;
  return <p className="meta">(no content)</p>;
}

// ------------------------------------------------------------------ helpers

/**
 * What the frame is not allowed to receive. It cannot run scripts anyway — no
 * allow-scripts — so this is the second lock, not the only one.
 */
function clean(html: string): string {
  const doc = new DOMParser().parseFromString(html, "text/html");
  for (const tag of ["script", "iframe", "object", "embed", "form", "link", "meta", "base", "frame", "frameset"]) {
    doc.querySelectorAll(tag).forEach((el) => el.remove());
  }
  const walker = doc.createTreeWalker(doc.documentElement, NodeFilter.SHOW_ELEMENT);
  let node = walker.nextNode() as Element | null;
  while (node) {
    node.removeAttribute("bgcolor");
    node.removeAttribute("color");
    for (const attr of [...node.attributes]) {
      const name = attr.name.toLowerCase();
      if (name.startsWith("on")) node.removeAttribute(attr.name);
      else if (name === "style") {
        // Colours the sender chose would fight the theme; the rest may stay.
        const kept = attr.value
          .split(";")
          .map((d) => d.trim())
          .filter((d) => d && !/^(color|background)/i.test(d.split(":")[0].trim()))
          .join("; ");
        if (kept) node.setAttribute("style", kept);
        else node.removeAttribute("style");
      } else if ((name === "href" || name === "src") && attr.value) {
        const v = attr.value.trim();
        if (v && !/^(https?:|mailto:|tel:|cid:|data:image\/|#|\/)/i.test(v)) node.removeAttribute(attr.name);
      }
    }
    node = walker.nextNode() as Element | null;
  }
  return doc.documentElement.outerHTML;
}

function readTheme() {
  const css = getComputedStyle(window.document.documentElement);
  const v = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback;
  return {
    bg: v("--ctp-mantle", "#181825"),
    text: v("--ctp-text", "#cdd6f4"),
    link: v("--ctp-blue", "#89b4fa"),
    muted: v("--ctp-overlay1", "#7f849c"),
    alt: v("--ctp-surface0", "#313244"),
  };
}

/** "Leon Feuerstein <a@b.c>" → "Leon Feuerstein" */
function senderName(from: string): string {
  const named = from.match(/^\s*"?([^"<]+?)"?\s*</);
  if (named) return named[1].trim();
  return from.replace(/[<>]/g, "").trim() || "(unknown)";
}

function initials(from: string): string {
  const name = senderName(from);
  const parts = name.split(/[\s.@_-]+/).filter(Boolean);
  const letters = (parts[0]?.[0] ?? "?") + (parts.length > 1 ? parts[parts.length - 1][0] : "");
  return letters.toUpperCase();
}

/** Today shows the time, this year the day, older the year. */
function shortDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  }
  if (d.getFullYear() === now.getFullYear()) {
    return d.toLocaleDateString(undefined, { day: "2-digit", month: "short" });
  }
  return d.toLocaleDateString(undefined, { month: "short", year: "numeric" });
}

function longDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    weekday: "short", day: "2-digit", month: "2-digit", year: "numeric",
    hour: "2-digit", minute: "2-digit", hour12: false,
  });
}

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} kB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function Compose({ project, onClose, onSent }: { project: Project; onClose: () => void; onSent: () => void }) {
  const accounts = useQuery<{ accounts: Account[] }>("/api/accounts");
  const [form, setForm] = useState({ account: "", to: "", subject: "", body: "" });
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const mailAccounts = (accounts.data?.accounts ?? []).filter((a) => a.kind === "mail");

  return (
    <Modal
      title="Write a message"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy || !form.account || !form.to}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/projects/${project.id}/mail/send`, { method: "POST", body: form });
                onSent();
                onClose();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Send
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Through which account">
        <select value={form.account} onChange={(e) => setForm({ ...form, account: e.target.value })}>
          <option value="">— pick one —</option>
          {mailAccounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.title}
              {a.needsSecret ? " (needs its password again)" : ""}
            </option>
          ))}
        </select>
      </Field>
      <Field label="To">
        <input value={form.to} onChange={(e) => setForm({ ...form, to: e.target.value })} />
      </Field>
      <Field label="Subject">
        <input value={form.subject} onChange={(e) => setForm({ ...form, subject: e.target.value })} />
      </Field>
      <Field label="Message">
        <textarea value={form.body} onChange={(e) => setForm({ ...form, body: e.target.value })} />
      </Field>
    </Modal>
  );
}
