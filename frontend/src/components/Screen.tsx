import { useEffect, useRef } from "react";
import { Terminal as Xterm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { tokenForUrl } from "../lib/api";

/**
 * The terminal itself: xterm on this side, a pty and tmux on the other, one
 * socket between them.
 *
 * Nothing in here interprets what comes back — colours, the cursor, vim, less,
 * a progress bar all work because the bytes are passed along untouched. The
 * password is never in the address: a browser cannot set headers on a socket,
 * so it goes as the first message and nothing is sent before it.
 */
export function Screen({
  base,
  session,
  password,
  byAccount,
  onNeedsPassword,
}: {
  base: string;
  session: string;
  password: string;
  byAccount?: boolean;
  onNeedsPassword?: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<Xterm | null>(null);
  const socket = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!host.current) return;
    if (!byAccount && !password) {
      onNeedsPassword?.();
      return;
    }

    const xterm = new Xterm({
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 13,
      cursorBlink: true,
      convertEol: false,
      theme: {
        background: "#11111b",
        foreground: "#cdd6f4",
        cursor: "#f5e0dc",
        selectionBackground: "#585b70",
      },
    });
    const fit = new FitAddon();
    xterm.loadAddon(fit);
    xterm.open(host.current);
    fit.fit();
    term.current = xterm;

    const url = new URL(
      `${base}/pty?session=${encodeURIComponent(session)}`,
      window.location.origin,
    );
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const token = tokenForUrl();
    if (token) url.searchParams.set("token", token);

    const ws = new WebSocket(url.toString());
    ws.binaryType = "arraybuffer";
    socket.current = ws;

    const size = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ cols: xterm.cols, rows: xterm.rows }));
      }
    };

    ws.onopen = () => {
      // The sign-in goes first, before a single keystroke.
      if (!byAccount) ws.send(JSON.stringify({ password }));
      size();
      xterm.focus();
    };
    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) xterm.write(new Uint8Array(event.data));
      else xterm.write(String(event.data));
    };
    ws.onclose = () => xterm.write("\r\n\x1b[2m— the connection closed —\x1b[0m\r\n");

    const typed = xterm.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data));
    });

    const watcher = new ResizeObserver(() => size());
    watcher.observe(host.current);
    window.addEventListener("resize", size);

    return () => {
      watcher.disconnect();
      window.removeEventListener("resize", size);
      typed.dispose();
      ws.close();
      xterm.dispose();
      term.current = null;
      socket.current = null;
    };
  }, [base, session, password, byAccount]);

  return <div className="screen" ref={host} />;
}
