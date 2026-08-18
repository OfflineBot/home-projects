import { useEffect, useRef } from "react";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { EditorState, Compartment } from "@codemirror/state";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { html } from "@codemirror/lang-html";
import { css } from "@codemirror/lang-css";
import { oneDark } from "@codemirror/theme-one-dark";
import { vim } from "@replit/codemirror-vim";

/**
 * A code editor inside a dialog: no toolbar, no save button, no file — it is a
 * field like any other and simply hands back what is in it.
 *
 * The vim switch is the same one the file editor keeps, so turning it on once
 * turns it on everywhere.
 */
export function CodeArea({
  value,
  language = "html",
  onChange,
  onReady,
  minHeight = 220,
}: {
  value: string;
  language?: "html" | "css";
  onChange: (text: string) => void;
  /** Hands back a way to put something in at the cursor. */
  onReady?: (api: { insert: (text: string) => void }) => void;
  minHeight?: number;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(onChange);
  latest.current = onChange;
  const keys = useRef(new Compartment());
  const vimOn = localStorage.getItem("editor.vim") === "on";

  useEffect(() => {
    if (!host.current) return;
    const state = EditorState.create({
      doc: value,
      extensions: [
        keys.current.of(vimOn ? vim() : []),
        lineNumbers(),
        history(),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        language === "css" ? css() : html(),
        oneDark,
        EditorView.lineWrapping,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) latest.current(update.state.doc.toString());
        }),
      ],
    });
    const editor = new EditorView({ state, parent: host.current });
    view.current = editor;
    onReady?.({
      insert: (text: string) => {
        const at = editor.state.selection.main;
        editor.dispatch({
          changes: { from: at.from, to: at.to, insert: text },
          selection: { anchor: at.from + text.length },
        });
        editor.focus();
      },
    });
    return () => {
      editor.destroy();
      view.current = null;
    };
    // The text is handed in once; the dialog owns it from then on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language]);

  return <div className="code-area" style={{ minHeight }} ref={host} />;
}
