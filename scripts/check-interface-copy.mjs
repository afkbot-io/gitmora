/** Check authored copy without parsing or translating source/user data at runtime. */
import { readdir, readFile } from "node:fs/promises";
import { parsers } from "prettier/plugins/typescript";

export async function interfaceMessages() {
  const messages = new Set();
  async function directory(root) {
    for (const item of await readdir(root, { withFileTypes: true })) {
      const path = `${root}/${item.name}`;
      if (item.isDirectory()) await directory(path);
      else if (/\.tsx?$/.test(path) && !/(?:messages|locale)\.ts$/.test(path)) {
        const ast = await parsers.typescript.parse(
          await readFile(path, "utf8"),
          {},
        );
        function walk(node) {
          if (!node || typeof node !== "object") return;
          if (node.type === "JSXText" && /[А-Яа-яЁё]/.test(node.value))
            throw new Error(
              `${path}:${node.loc.start.line}: unlocalized interface text`,
            );
          if (
            node.type === "Literal" &&
            typeof node.value === "string" &&
            /[А-Яа-яЁё]/.test(node.value)
          )
            messages.add(node.value);
          for (const [key, value] of Object.entries(node)) {
            if (["loc", "range", "comments", "tokens"].includes(key)) continue;
            if (Array.isArray(value)) value.forEach(walk);
            else if (value && typeof value === "object") walk(value);
          }
        }
        walk(ast);
      }
    }
  }
  await directory("web/src");
  return messages;
}
if (process.argv[1]?.endsWith("check-interface-copy.mjs")) {
  const { english } =
    await import("../build/web-tests/web/src/domain/messages.js");
  const messages = await interfaceMessages();
  const missing = [...messages].filter((text) => !english[text]);
  const unused = Object.keys(english).filter((text) => !messages.has(text));
  if (missing.length || unused.length)
    throw new Error(JSON.stringify({ missing, unused }, null, 2));
  for (const message of messages) {
    const placeholders = (text) =>
      [...text.matchAll(/\{\d+\}/g)]
        .map((m) => m[0])
        .sort()
        .join();
    if (placeholders(message) !== placeholders(english[message]))
      throw new Error(`Translation placeholders differ: ${message}`);
  }
  console.log(
    `Interface copy: ${messages.size} messages covered; no stale translations.`,
  );
}
