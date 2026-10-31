const paths = {
  setup: "M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5",
  work: "M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01",
  context: "m21 21-4.5-4.5M19 10.5a8.5 8.5 0 1 1-17 0 8.5 8.5 0 0 1 17 0",
  repositories:
    "M6 3v12a4 4 0 0 0 4 4h4M6 7h8a4 4 0 0 1 4 4v2M3 3h6M15 19h6M15 13h6",
  settings: "M4 7h16M4 17h16M8 4v6m8 4v6",
  catalog: "M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z",
  accounts:
    "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.87M15 3.13a4 4 0 0 1 0 7.75M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  bindings:
    "M10 13a5 5 0 0 0 7 .5l3-3a5 5 0 0 0-7-7l-2 2M14 11a5 5 0 0 0-7-.5l-3 3a5 5 0 0 0 7 7l2-2",
  keys: "m21 2-2 2m0 0-3 3m3-3 2 2M7.5 10a6.5 6.5 0 1 0 6.5 6.5c0-1.2-.3-2.4-.9-3.4L19 7l-2-2-6.1 6.1A6.5 6.5 0 0 0 7.5 10Z",
} as const;

export function Icon({ name }: { name: keyof typeof paths }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="shrink-0"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
