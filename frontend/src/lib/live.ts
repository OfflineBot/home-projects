import { useEffect, useRef } from "react";
import { tokenForUrl } from "./api";

/**
 * What is happening, while the page is open.
 *
 * The server publishes what it did — a file written, a variable's new value, a
 * scheduler finished — and this hands those to whoever is drawing something
 * that would otherwise be out of date until the next reload. No state comes
 * down this pipe: an event says what happened and the page asks for the part
 * that changed, which is why one stream is enough for the whole app.
 *
 * One connection, however many listeners. Opening an EventSource per card
 * would be a socket per card, and browsers stop at six per host.
 */

export interface LiveEvent {
  kind: string;
  projectId?: string;
  path?: string;
  detail?: Record<string, unknown>;
  at: string;
}

type Listener = (event: LiveEvent) => void;

const listeners = new Set<Listener>();
let source: EventSource | null = null;

function open() {
  if (source || typeof EventSource === "undefined") return;
  // A browser cannot put a header on an EventSource, so the token travels the
  // way it does for the terminal.
  const token = tokenForUrl();
  source = new EventSource(`/api/events${token ? `?token=${encodeURIComponent(token)}` : ""}`);
  source.onmessage = (message) => {
    let event: LiveEvent;
    try {
      event = JSON.parse(message.data) as LiveEvent;
    } catch {
      return;
    }
    for (const listener of [...listeners]) listener(event);
  };
  // The browser reconnects by itself; nothing to do but let it.
}

function close() {
  if (listeners.size) return;
  source?.close();
  source = null;
}

/**
 * Hear about what happens. The callback may change between renders — what is
 * registered is a stable wrapper, so listening is not torn down and set up
 * again on every draw.
 */
export function useLive(onEvent: Listener) {
  const held = useRef(onEvent);
  held.current = onEvent;
  useEffect(() => {
    const listener: Listener = (event) => held.current(event);
    listeners.add(listener);
    open();
    return () => {
      listeners.delete(listener);
      close();
    };
  }, []);
}
