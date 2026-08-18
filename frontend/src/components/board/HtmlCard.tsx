import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Suspense } from "react";
import { cardViews, format, type CardProps } from "./cards";

/**
 * A piece of a page, written by hand.
 *
 * Two ways, and the difference matters:
 *
 *   - Part of the board. The markup is cleaned — no scripts, no frames, no
 *     event handlers — and then it is simply in the page, so it inherits the
 *     theme and sits among the other cards as if it belonged there.
 *   - A frame of its own. Everything is allowed inside, including CSS and
 *     scripts, because it runs sandboxed: no access to this page, no cookies,
 *     no session. That is the price of the freedom and it is worth saying out
 *     loud rather than pretending both are the same thing.
 *
 * A board is somebody's own page, and this is what makes it one.
 */
export default function HtmlCard({ options, value, projects, editing }: CardProps) {
  // A page is not a picture of the system: {{dhbw/noten.average}} is the
  // number as it is now, and <hp-card …> is a real card in the middle of the
  // text. Both are why a hand-written page stays alive.
  const source = fill(String(options.html ?? ""), value);
  const frame = String(options.mode ?? "inline") === "frame";
  const holder = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(0);

  const document = useMemo(() => {
    if (!frame) return "";
    const colours = readTheme();
    return `<!doctype html><html><head><meta charset="utf-8"><base target="_blank">
      <style>
        :root{color-scheme:dark;--text:${colours.text};--bg:${colours.bg};--accent:${colours.accent};
              --muted:${colours.muted};--surface:${colours.surface}}
        html,body{margin:0;padding:0;background:${colours.bg};color:${colours.text};
          font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;font-size:15px;line-height:1.55}
        a{color:${colours.accent}}
      </style></head><body>${source}</body></html>`;
  }, [source, frame]);

  // The frame says how tall it is, so a card can hold a page that grows.
  useEffect(() => {
    if (!frame) return;
    const fit = () => {
      const body = holder.current?.contentDocument?.body;
      if (body) setHeight(body.scrollHeight);
    };
    const timer = setInterval(fit, 1000);
    return () => clearInterval(timer);
  }, [frame, document]);

  // Where a page asked for a card, the card is mounted into that spot.
  const [slots, setSlots] = useState<{ node: Element; kind: string; options: Record<string, string> }[]>([]);
  const page = useRef<HTMLDivElement>(null);
  const cleaned = useMemo(() => (frame ? "" : clean(source)), [source, frame]);

  // The page is put into the DOM here rather than by React.
  //
  // React would set this markup again on the next render, and the nodes the
  // cards are mounted into would be replaced underneath them — which is
  // precisely how a written <hp-card> stayed an empty tag while everything
  // about it was right. Owning the subtree ourselves keeps those nodes alive.
  useEffect(() => {
    if (frame || !page.current) return;
    page.current.innerHTML = cleaned;
    const found = [...page.current.querySelectorAll("hp-card")].map((node) => {
      const options: Record<string, string> = {};
      for (const attr of [...node.attributes]) options[attr.name] = attr.value;
      const kind = options.kind ?? "";
      delete options.kind;
      // A card brought from the page keeps its own box; the page decides where.
      node.innerHTML = "";
      return { node, kind, options };
    });
    setSlots(found);
  }, [cleaned, frame]);

  if (!source.trim()) {
    return <div className="meta">Nothing written yet — open this card's settings.</div>;
  }

  if (frame) {
    return (
      <iframe
        ref={holder}
        className="html-frame"
        title="A page of your own"
        sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
        srcDoc={document}
        style={height ? { minHeight: Math.min(height + 8, 4000) } : undefined}
        onLoad={() => {
          const body = holder.current?.contentDocument?.body;
          if (body) setHeight(body.scrollHeight);
        }}
      />
    );
  }

  return (
    <>
      <div className="html-inline" ref={page} />
      {slots.map((slot, i) => {
        const View = cardViews[slot.kind];
        if (!View) return null;
        return createPortal(
          <Suspense fallback={null}>
            <div className="card html-slot">
              <View
                options={{ ...slot.options, projectId: slot.options.project ?? slot.options.projectId }}
                value={value}
                projects={projects}
                editing={editing}
              />
            </div>
          </Suspense>,
          slot.node,
          `slot-${i}`,
        );
      })}
    </>
  );
}

/**
 * {{group/project.variable}} becomes what that variable says right now.
 *
 * The braces are the whole language on purpose: an assistant writing a page
 * should not have to learn a template engine, and a person reading the source
 * should be able to guess what it does.
 */
export function fill(html: string, value: CardProps["value"]): string {
  return html.replace(/\{\{([^}]+)\}\}/g, (whole, reference: string) => {
    const name = reference.trim();
    // "group/project.variable" or "project.variable" — the group is optional
    // because a board usually lives in one.
    const [head, ...rest] = name.split("/");
    const variable = rest.length ? rest.join("/") : head;
    const found = value(variable);
    if (found === undefined) return whole;
    const shown = format(found.value);
    return found.unit ? `${shown} ${found.unit}` : shown;
  });
}

function readTheme() {
  const css = getComputedStyle(window.document.documentElement);
  const v = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback;
  return {
    bg: v("--ctp-mantle", "#181825"),
    surface: v("--ctp-surface0", "#313244"),
    text: v("--ctp-text", "#cdd6f4"),
    accent: v("--ctp-blue", "#89b4fa"),
    muted: v("--ctp-overlay1", "#7f849c"),
  };
}

/**
 * What may be in the page itself. Layout, tables, images, links and inline
 * styles stay; anything that runs or reaches out does not — that is what the
 * frame is for.
 */
export function clean(html: string): string {
  const doc = new DOMParser().parseFromString(html, "text/html");
  for (const tag of ["script", "iframe", "object", "embed", "form", "link", "meta", "base", "frame", "frameset"]) {
    doc.querySelectorAll(tag).forEach((el) => el.remove());
  }
  const walker = doc.createTreeWalker(doc.documentElement, NodeFilter.SHOW_ELEMENT);
  let node = walker.nextNode() as Element | null;
  while (node) {
    for (const attr of [...node.attributes]) {
      const name = attr.name.toLowerCase();
      if (name.startsWith("on")) node.removeAttribute(attr.name);
      else if ((name === "href" || name === "src") && attr.value) {
        const value = attr.value.trim();
        if (value && !/^(https?:|mailto:|tel:|data:image\/|#|\/)/i.test(value)) node.removeAttribute(attr.name);
      }
    }
    node = walker.nextNode() as Element | null;
  }
  return doc.body.innerHTML;
}
