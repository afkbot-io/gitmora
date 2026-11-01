/** Build an isolated, content-addressed theme bundle without rewriting committed sources. */
import { build } from "vite";
import tailwindcss from "@tailwindcss/vite";
import {
  copyFile,
  chmod,
  cp,
  mkdir,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import { createHash } from "node:crypto";
import { basename, resolve } from "node:path";

const source = resolve("deploy/identity/themes/gitmora");
const bundle = resolve("build/identity-theme/gitmora");
const target = resolve(bundle, "login/resources");
await rm(bundle, { recursive: true, force: true });
await cp(source, bundle, {
  recursive: true,
  filter: (path) => path !== resolve(source, "login/resources"),
});
await mkdir(target, { recursive: true });
await build({
  configFile: false,
  base: "./",
  plugins: [tailwindcss()],
  build: {
    outDir: target,
    emptyOutDir: true,
    assetsInlineLimit: 0,
    rollupOptions: {
      input: resolve("deploy/identity/theme.css"),
      output: {
        assetFileNames: (asset) =>
          asset.names?.some((name) => name.endsWith(".css"))
            ? "css/gitmora-[hash].css"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
});
const styles = (await readdir(resolve(target, "css"))).filter((name) =>
  /^gitmora-[\w-]+\.css$/.test(name),
);
await mkdir(resolve(target, "img"), { recursive: true });
await copyFile(
  resolve("web/public/assets/workgraph.svg"),
  resolve(target, "img/favicon.svg"),
);
if (styles.length !== 1)
  throw new Error("Expected exactly one generated identity stylesheet");
const script = await readFile(resolve("web/public/assets/theme.js"));
const scriptName = `js/theme-${createHash("sha256").update(script).digest("hex").slice(0, 16)}.js`;
await mkdir(resolve(target, "js"), { recursive: true });
await writeFile(resolve(target, scriptName), script);
const localeScript = await readFile(resolve("web/public/assets/locale.js"));
const localeScriptName = `js/locale-${createHash("sha256").update(localeScript).digest("hex").slice(0, 16)}.js`;
await writeFile(resolve(target, localeScriptName), localeScript);
const properties = await readFile(
  resolve(source, "login/theme.properties"),
  "utf8",
);
if (
  !properties.includes("styles=css/gitmora.css\n") ||
  !properties.includes("gitmoraThemeScript=js/theme.js\n") ||
  !properties.includes("gitmoraLocaleScript=js/locale.js\n")
)
  throw new Error("Theme source asset contract changed");
await writeFile(
  resolve(bundle, "login/theme.properties"),
  properties
    .replace("styles=css/gitmora.css", `styles=css/${styles[0]}`)
    .replace(
      "gitmoraThemeScript=js/theme.js",
      `gitmoraThemeScript=${scriptName}`,
    )
    .replace(
      "gitmoraLocaleScript=js/locale.js",
      `gitmoraLocaleScript=${localeScriptName}`,
    ),
);
for (const name of ["ibm-plex-sans", "ibm-plex-mono"]) {
  await copyFile(
    resolve(`node_modules/@fontsource/${name}/LICENSE`),
    resolve(target, `${name}-LICENSE.txt`),
  );
}
await copyFile(
  resolve("node_modules/tailwindcss/LICENSE"),
  resolve(target, "tailwind-LICENSE.txt"),
);
// A private source archive can contain 0700 directories. Theme resources are
// public output and must remain readable by the provider's nonroot runtime.
async function publicPermissions(directory) {
  await chmod(directory, 0o755);
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) await publicPermissions(path);
    else if (entry.isFile()) await chmod(path, 0o644);
    else throw new Error("Unexpected identity theme output type");
  }
}
await publicPermissions(bundle);
console.log(
  JSON.stringify({
    theme: basename(bundle),
    styles: styles[0],
    script: scriptName,
    localeScript: localeScriptName,
  }),
);
