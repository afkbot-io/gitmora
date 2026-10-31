/** Remove only generated asset output; keep the clean-checkout embedding sentinel. */
import { readdir, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { build } from "vite";
const target = resolve("internal/transport/webui/assets");
for (const name of await readdir(target))
  if (name !== "README.txt")
    await rm(resolve(target, name), { recursive: true, force: true });
await build({ configFile: "web/vite.config.ts" });
const packages = [
  "react",
  "react-dom",
  "scheduler",
  "@fontsource/ibm-plex-sans",
  "@fontsource/ibm-plex-mono",
  "tailwindcss",
];
const notices = await Promise.all(
  packages.map(
    async (name) =>
      name +
      "\n\n" +
      (await readFile(resolve("node_modules", name, "LICENSE"), "utf8")),
  ),
);
await writeFile(
  resolve(target, "assets/third-party-licenses.txt"),
  notices.join("\n\n--------------------\n\n"),
);
