// A small hand-drawn icon set — one stroke style, no icon package, no font.
// Everything is currentColor, so icons follow the flavour like the rest.

const paths: Record<string, string> = {
  folder: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
  box: "M21 8 12 3 3 8v8l9 5 9-5zM3 8l9 5 9-5M12 13v8",
  calendar: "M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v13a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2zM4 9h16M8 3v4M16 3v4",
  notebook: "M6 3h11a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6zM6 3v18M9 8h7M9 12h7",
  award: "M12 3a6 6 0 1 0 0 12 6 6 0 0 0 0-12zM8.5 14 7 22l5-2.5L17 22l-1.5-8",
  globe: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c2.5 3 2.5 15 0 18M12 3c-2.5 3-2.5 15 0 18",
  mail: "M3 7a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM3 7l9 6 9-6",
  rss: "M5 19a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM5 12a7 7 0 0 1 7 7M5 5a14 14 0 0 1 14 14",
  cpu: "M7 7h10v10H7zM4 9h3M4 15h3M17 9h3M17 15h3M9 4v3M15 4v3M9 17v3M15 17v3",
  flame: "M12 22c4 0 6-2.7 6-6 0-4-4-5-4-9 0 0-3 1.5-3 5 0 1.5-1 2-1.5 1.2C9 12 8.5 11 8.5 10 7 11.5 6 13.7 6 16c0 3.3 2 6 6 6z",
  lightbulb: "M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5.9 1.2.9 2h5.2c0-.8.3-1.5.9-2A6 6 0 0 0 12 3z",
  server: "M4 5h16v5H4zM4 14h16v5H4zM7 7.5h.01M7 16.5h.01",
  code: "m9 17-5-5 5-5M15 7l5 5-5 5",
  database: "M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  graduation: "m3 9 9-4 9 4-9 4zM7 11.5V17c0 1 2.2 2 5 2s5-1 5-2v-5.5",
  home: "m4 11 8-7 8 7M6 10v9a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-9",
  music: "M9 18V6l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 16a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z",
  camera: "M4 8a2 2 0 0 1 2-2h2l1.5-2h5L16 6h2a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2zM12 16a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z",
  heart: "M12 20s-7-4.4-7-9.3A3.8 3.8 0 0 1 12 8a3.8 3.8 0 0 1 7 2.7C19 15.6 12 20 12 20z",
  star: "m12 4 2.4 5 5.6.8-4 3.9 1 5.5-5-2.7-5 2.7 1-5.5-4-3.9 5.6-.8z",
  wrench: "M15 4a5 5 0 0 0-5 6.4L4 16.4V20h3.6l6-6A5 5 0 1 0 15 4z",
  zap: "M13 3 5 14h6l-1 7 8-11h-6z",
  lock: "M6 11h12v9H6zM9 11V8a3 3 0 0 1 6 0v3",
  users: "M8 12a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM3 20c0-2.8 2.2-5 5-5s5 2.2 5 5M16 6.5a3 3 0 0 1 0 6M18 20c0-2-.8-3.8-2-5",
  plus: "M12 5v14M5 12h14",
  more: "M12 6.5h.01M12 12h.01M12 17.5h.01",
  chevronRight: "m9 6 6 6-6 6",
  chevronLeft: "m15 6-6 6 6 6",
  chevronDown: "m6 9 6 6 6-6",
  trash: "M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13M10 11v6M14 11v6",
  upload: "M12 16V4M8 8l4-4 4 4M4 17v2a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-2",
  download: "M12 4v12M8 12l4 4 4-4M4 17v2a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-2",
  settings: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 13a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-2.7 1.1v.3a2 2 0 1 1-4 0V19a1.6 1.6 0 0 0-2.7-1.1l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.6 1.6 0 0 0 4.6 13H4a2 2 0 1 1 0-4h.2a1.6 1.6 0 0 0 1.1-2.7l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.6 1.6 0 0 0 11 4.6V4a2 2 0 1 1 4 0v.2a1.6 1.6 0 0 0 2.7 1.1l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0 1.1 2.7h.2a2 2 0 1 1 0 4h-.2a1.6 1.6 0 0 0-1.2.2z",
  check: "m5 13 4 4L19 7",
  x: "M6 6l12 12M18 6 6 18",
  file: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5",
  link: "M10 13a4 4 0 0 0 5.7 0l3-3A4 4 0 0 0 13 4.4l-1.6 1.6M14 11a4 4 0 0 0-5.7 0l-3 3A4 4 0 0 0 11 19.6l1.6-1.6",
  refresh: "M20 11a8 8 0 1 0-1.8 6M20 5v6h-6",
  play: "m7 4 12 8-12 8z",
  git: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM9 8.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM15 8.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM9 11.5v1.2c0 1.3 1 2.3 2.3 2.3H15M12 15v3",
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
  menu: "M4 7h16M4 12h16M4 17h16",
  eye: "M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2",
  archive: "M4 5h16v4H4zM6 9v10h12V9M10 13h4",
  copy: "M9 9h10v12H9zM5 15V3h10v2",
  map: "M9 4 3 6v14l6-2 6 2 6-2V4l-6 2zM9 4v14M15 6v14",
  key: "M15 4a5 5 0 1 1-4.6 7L4 17.4V21h3.6l1-1v-2h2l1.4-1.4A5 5 0 0 1 15 4z",
  alert: "M12 4 2 20h20zM12 10v4M12 17.5h.01",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v5M12 8h.01",
  grid: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  logout: "M10 20H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h5M16 16l4-4-4-4M20 12H10",
};

export type IconName = keyof typeof paths | string;

export function Icon({
  name,
  size = 18,
  strokeWidth = 1.7,
  className,
}: {
  name: IconName;
  size?: number;
  strokeWidth?: number;
  className?: string;
}) {
  const d = paths[name] ?? paths.box;
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {d.split("M").filter(Boolean).map((segment, i) => (
        <path key={i} d={"M" + segment} />
      ))}
    </svg>
  );
}

export const iconNames = Object.keys(paths);
