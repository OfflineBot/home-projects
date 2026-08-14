import { useEffect, useRef, useState } from "react";
import { EditorView, keymap, lineNumbers, highlightActiveLine } from "@codemirror/view";
import { EditorState, Compartment } from "@codemirror/state";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { oneDark } from "@codemirror/theme-one-dark";
import { vim } from "@replit/codemirror-vim";
import { Icon } from "./Icon";

/**
 * The editor for a file in a project: notes, a vault, a bit of code.
 *
 * Vim keys are a switch, not a religion — off by default, on for whoever wants
 * them, and remembered. Saving is Ctrl-S or the button; with vim on, :w does
 * it too, because anybody who turns vim on will type that.
 *
 * It says out loud whether what is on screen is what is on disk. An editor that
 * hides that is one you stop trusting after the first lost paragraph.
 */
export function Editor({
  value,
  path,
  onSave,
  busy,
}: {
  value: string;
  path: string;
  onSave: (text: string) => Promise<void>;
  busy?: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const saved = useRef(value);
  const [dirty, setDirty] = useState(false);
  const [vimOn, setVimOn] = useState(() => localStorage.getItem("editor.vim") === "on");
  const keys = useRef(new Compartment());
  const save = useRef(onSave);
  save.current = onSave;

  useEffect(() => {
    if (!host.current) return;

    const language =
      path.endsWith(".md") ? markdown()
      : path.endsWith(".json") ? json()
      : /\.(ts|tsx|js|jsx)$/.test(path) ? javascript({ typescript: path.endsWith("ts") || path.endsWith("tsx") })
      : [];

    const store = async (text: string) => {
      await save.current(text);
      saved.current = text;
      setDirty(false);
    };

    const state = EditorState.create({
      doc: value,
      extensions: [
        keys.current.of(vimOn ? vim() : []),
        lineNumbers(),
        highlightActiveLine(),
        history(),
        keymap.of([
          {
            key: "Mod-s",
            preventDefault: true,
            run: (v) => {
              void store(v.state.doc.toString());
              return true;
            },
          },
          ...defaultKeymap,
          ...historyKeymap,
          indentWithTab,
        ]),
        language,
        oneDark,
        EditorView.lineWrapping,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) setDirty(update.state.doc.toString() !== saved.current);
        }),
      ],
    });

    const editor = new EditorView({ state, parent: host.current });
    view.current = editor;
    // :w and :wq, for whoever has vim on.
    (window as any).CodeMirrorVimSave = () => void store(editor.state.doc.toString());
    return () => {
      editor.destroy();
      view.current = null;
    };
    // The document is set once; changing files remounts through `key`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  useEffect(() => {
    view.current?.dispatch({ effects: keys.current.reconfigure(vimOn ? vim() : []) });
    localStorage.setItem("editor.vim", vimOn ? "on" : "off");
  }, [vimOn]);

  return (
    <div className="editor">
      <div className="editor-bar">
        <span className="mono grow">{path}</span>
        {dirty ? <span className="badge warn">not saved</span> : <span className="meta">saved</span>}
        <label className="check" style={{ margin: 0 }} title="Vim keys">
          <input type="checkbox" checked={vimOn} onChange={(e) => setVimOn(e.target.checked)} />
          <span className="meta">vim</span>
        </label>
        <button
          className="btn small primary"
          disabled={busy || !dirty}
          onClick={async () => {
            const text = view.current?.state.doc.toString() ?? "";
            await save.current(text);
            saved.current = text;
            setDirty(false);
          }}
        >
          <Icon name="check" size={14} /> Save
        </button>
      </div>
      <div className="editor-host" ref={host} />
    </div>
  );
}
