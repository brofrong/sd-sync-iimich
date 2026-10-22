// Line icons (24×24, stroked) as SVG path data.
export const icons = {
  status: "M3 12h4l3-8 4 16 3-8h4",
  history: "M12 3a9 9 0 1 1 0 18a9 9 0 1 1 0-18zM12 7v5l3.5 2",
  cards: "M8 3h7l4 4v13a1 1 0 0 1-1 1H8a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM10 3v4M13 3v4M16 5v2",
  server:
    "M5 4h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM5 14h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1zM8 7h.01M8 17h.01",
  logs: "M7 3h7l5 5v12a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM14 3v5h5M9 13h6M9 17h6",
  settings:
    "M4 6h9m4 0h3M4 12h3m4 0h9M4 18h11m4 0h1M17 6a2 2 0 1 1-4 0a2 2 0 1 1 4 0zM11 12a2 2 0 1 1-4 0a2 2 0 1 1 4 0zM19 18a2 2 0 1 1-4 0a2 2 0 1 1 4 0z",
  clock: "M12 3a9 9 0 1 1 0 18a9 9 0 1 1 0-18zM12 7v5l3.5 2",
  warning: "M12 3.5l9.5 16.5h-19zM12 10v4.5M12 17.5h.01",
  pause: "M12 3a9 9 0 1 1 0 18a9 9 0 1 1 0-18zM10 9v6M14 9v6",
} as const;

export type IconName = keyof typeof icons;
