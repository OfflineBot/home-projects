import { useEffect, useRef, useState } from "react";
import { Terminal as Xterm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { tokenForUrl } from "../../lib/api";

/**
 * The terminal emulator itself. One of these, everywhere.
 *
 * This is the piece that talks: xterm on this side, a pty with tmux on the
 * other, one socket between them, and nothing in the middle interpreting what
 * comes back — colours, the cursor, vim, less, a progress bar all work because
 * the bytes are passed along untouched.
 *
 * It draws no chrome at all: no bar, no title, no session list. Whatever puts
 * one on the screen decides what is around it, which is what makes a terminal
 * in a card, a terminal on the machines page and a terminal in a hand-written
 * page the same terminal. Fixing this fixes all three.
 *
 * Two things it is careful about, both of which it used to get wrong:
 *
 *   - It never grows its own box. The screen is positioned inside a container
 *     it does not size, so measuring gives the space it was given rather than
 *     the space it just asked for. Anything else is a feedback loop, and a
 *     terminal in a small card ended up drawn at the wrong size.
 *   - It fits after the fonts have loaded. Measuring a monospace box before the
 *     font is there gives the wrong column count, and the picture stays wrong
 *     until something else happens to resize it.
 *
 * The password is never in the address: a browser cannot set headers on a
 * socket, so it goes as the first message and nothing is sent before it.
 */
export function Emulator({
  base,
  session,
  password,
  byAccount,
  size,
  onNeedsPassword,
  onMeasured,
}: {
  /** /api/projects/:id/machines/:name */
  base: string;
  session: string;
  password: string;
  byAccount?: boolean;
  /** Type size in pixels. 0 means: choose one that suits the box. */
  size?: number;
  onNeedsPassword?: () => void;
  /** How wide and tall the terminal came out, in characters. */
  onMeasured?: (cols: number, rows: number) => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const told = useRef(onMeasured);
  told.current = onMeasured;
  const host = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<"opening" | "live" | "closed">("opening");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!host.current || !box.current) return;
    if (!byAccount && !password) {
      onNeedsPassword?.();
      return;
    }
    setState("opening");

    // A small box gets a smaller face, so a card two rows high is still a
    // terminal of some width rather than forty columns of nothing. A size that
    // was asked for wins over that.
    const wide = box.current.clientWidth;
    const fontSize = size && size > 0 ? size : wide < 380 ? 11 : wide < 620 ? 12 : 13;

    const xterm = new Xterm({
      fontFamily: 'ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, monospace',
      fontSize,
      lineHeight: 1.15,
      cursorBlink: true,
      cursorStyle: "bar",
      convertEol: false,
      scrollback: 5000,
      drawBoldTextInBrightColors: true,
      theme: {
        background: "#11111b",
        foreground: "#cdd6f4",
        cursor: "#f5e0dc",
        cursorAccent: "#11111b",
        selectionBackground: "#45475a",
        black: "#45475a", red: "#f38ba8", green: "#a6e3a1", yellow: "#f9e2af",
        blue: "#89b4fa", magenta: "#f5c2e7", cyan: "#94e2d5", white: "#bac2de",
        brightBlack: "#585b70", brightRed: "#f38ba8", brightGreen: "#a6e3a1",
        brightYellow: "#f9e2af", brightBlue: "#89b4fa", brightMagenta: "#f5c2e7",
        brightCyan: "#94e2d5", brightWhite: "#a6adc8",
      },
    });
    const fitter = new FitAddon();
    xterm.loadAddon(fitter);
    xterm.open(host.current);

    const url = new URL(`${base}/pty?session=${encodeURIComponent(session)}`, window.location.origin);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const token = tokenForUrl();
    if (token) url.searchParams.set("token", token);

    const ws = new WebSocket(url.toString());
    ws.binaryType = "arraybuffer";

    const later: number[] = [];
    let frame = 0;
    const fit = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const width = box.current?.clientWidth ?? 0;
        const height = box.current?.clientHeight ?? 0;
        if (width < 40 || height < 20) return;
        try {
          fitter.fit();
        } catch {
          return; // measured mid-layout; the next resize will do it
        }
        if (xterm.cols > 0 && xterm.rows > 0) {
          told.current?.(xterm.cols, xterm.rows);
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ cols: xterm.cols, rows: xterm.rows }));
          }
        }
      });
    };

    fit();
    // The first measurement is wrong until the monospace font is really there.
    void (window.document as Document & { fonts?: FontFaceSet }).fonts?.ready.then(fit);

    ws.onopen = () => {
      // The sign-in goes first, before a single keystroke.
      if (!byAccount) ws.send(JSON.stringify({ password }));
      setState("live");
      fit();
      xterm.focus();
      // Again, a moment later. The size is sent while the other side is still
      // signing in and starting tmux; if that one is missed the session stays
      // at the size the pty was asked for, and the picture stays narrower than
      // the window for as long as it is open. Saying it twice costs nothing.
      later.push(window.setTimeout(fit, 600), window.setTimeout(fit, 2000));
    };
    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) xterm.write(new Uint8Array(event.data));
      else xterm.write(String(event.data));
    };
    ws.onclose = () => setState("closed");
    ws.onerror = () => setState("closed");

    const typed = xterm.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data));
    });

    const watcher = new ResizeObserver(fit);
    watcher.observe(box.current);
    window.addEventListener("resize", fit);

    return () => {
      cancelAnimationFrame(frame);
      for (const timer of later) window.clearTimeout(timer);
      watcher.disconnect();
      window.removeEventListener("resize", fit);
      typed.dispose();
      ws.onclose = null;
      ws.close();
      xterm.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, session, password, byAccount, size, attempt]);

  return (
    <div className="terminal-screen" ref={box}>
      <div className="terminal-xterm" ref={host} />
      {state !== "live" ? (
        <div className="terminal-over">
          {state === "opening" ? (
            <span className="meta">opening…</span>
          ) : (
            <button className="btn small" onClick={() => setAttempt((n) => n + 1)}>
              The connection closed — open it again
            </button>
          )}
        </div>
      ) : null}
    </div>
  );
}
