// Catppuccin is the colour basis, Mocha the default.
//
// All four flavours are switchable — and dark stays the default even when the
// visitor's system says otherwise. The choice belongs to the user, not to the
// device, so it is stored locally and applies to visitors without an account
// too.

import { useSyncExternalStore } from "react";

export const FLAVORS = [
  { key: "mocha", title: "Mocha", note: "dark · the default", dark: true },
  { key: "macchiato", title: "Macchiato", note: "dark · a touch softer", dark: true },
  { key: "frappe", title: "Frappé", note: "dark · warmer", dark: true },
  { key: "latte", title: "Latte", note: "light", dark: false },
] as const;

export type Flavor = (typeof FLAVORS)[number]["key"];

/** Every accent Catppuccin offers. Group and project colours use the same set. */
export const ACCENTS = [
  "rosewater", "flamingo", "pink", "mauve", "red", "maroon", "peach",
  "yellow", "green", "teal", "sky", "sapphire", "blue", "lavender",
] as const;

export type Accent = (typeof ACCENTS)[number];

const listeners = new Set<() => void>();

function read(key: string, fallback: string) {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

let flavor: Flavor = read("flavor", "mocha") as Flavor;
let accent: Accent = read("accent", "mauve") as Accent;

function apply() {
  document.documentElement.dataset.flavor = flavor;
  document.documentElement.dataset.accent = accent;
  listeners.forEach((fn) => fn());
}

export function setFlavor(next: Flavor) {
  flavor = next;
  try {
    localStorage.setItem("flavor", next);
  } catch {
    /* private mode — the choice then lasts for this session only */
  }
  apply();
}

export function setAccent(next: Accent) {
  accent = next;
  try {
    localStorage.setItem("accent", next);
  } catch {
    /* see above */
  }
  apply();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useTheme() {
  const current = useSyncExternalStore(
    subscribe,
    () => `${flavor}|${accent}`,
    () => "mocha|mauve",
  );
  const [f, a] = current.split("|");
  return { flavor: f as Flavor, accent: a as Accent, setFlavor, setAccent };
}

/** The CSS variable a palette colour maps to. */
export function colorVar(name?: string) {
  if (!name) return "var(--accent)";
  return `var(--ctp-${name})`;
}

apply();
