/** Remove only generated asset output; keep the clean-checkout embedding sentinel. */
import { readdir, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { build } from "vite";
const target = resolve("internal/transport/webui/assets");
for (const name of await readdir(target))
  if (name !== "README.txt")
    await rm(resolve(target, name), { recursive: true, force: true });
await build({ configFile: "web/vite.config.ts" });
// Include license texts of all shipped runtime dependencies, including Markdown's
// parser tree. Keep the build-only stylesheet compiler notice as before.
const lock = JSON.parse(await readFile("package-lock.json", "utf8"));
const paths = new Set(
  Object.entries(lock.packages)
    .filter(([path, item]) => path && !item.dev && !item.link)
    .map(([path]) => path),
);
paths.add("node_modules/tailwindcss");
const notices = await Promise.all(
  [...paths].sort().map(async (path) => {
    const pkg = JSON.parse(
      await readFile(resolve(path, "package.json"), "utf8"),
    );
    const filenames = (await readdir(resolve(path))).filter((name) =>
      /^licen[sc]e(?:[.-].*)?$/i.test(name),
    );
    const texts = await Promise.all(
      filenames.map((name) => readFile(resolve(path, name), "utf8")),
    );
    if (!texts.length) throw new Error(`Missing runtime license: ${pkg.name}`);
    return pkg.name + "\n\n" + texts.join("\n");
  }),
);
await writeFile(
  resolve(target, "assets/third-party-licenses.txt"),
  notices.join("\n\n--------------------\n\n"),
);
