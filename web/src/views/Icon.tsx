const paths = {
  chevron: "m9 5 7 7-7 7",
  setup: "M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5",
  context: "m21 21-4.5-4.5M19 10.5a8.5 8.5 0 1 1-17 0 8.5 8.5 0 0 1 17 0",
  repositories:
    "M6 3v12a4 4 0 0 0 4 4h4M6 7h8a4 4 0 0 1 4 4v2M3 3h6M15 19h6M15 13h6",
  settings: "M4 7h16M4 17h16M8 4v6m8 4v6",
  members:
    "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.87M15 3.13a4 4 0 0 1 0 7.75M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
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
