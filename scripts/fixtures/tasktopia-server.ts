/** Isolated contract harness: run from a disposable Tasktopia source copy, never its working directory. */
import Fastify from "fastify";
import { createDb } from "./src/server/db";
import { registerUser, createMcpToken } from "./src/server/auth";
import { AppService } from "./src/server/app-service";
import { registerMcpHttp } from "./src/server/mcp-http";

if (!process.env.TEST_DATABASE_URL || !process.env.APP_ORIGIN || !/^tasktopia_contract_[a-f0-9]{32}$/.test(process.env.TASKTOPIA_TEST_SCHEMA ?? "")) throw new Error("Explicit isolated configuration required");
const db = await createDb(process.env.TEST_DATABASE_URL, { schema: process.env.TASKTOPIA_TEST_SCHEMA });
const app = Fastify({ logger: false });
let closed = false;
async function close() {
  if (closed) return;
  closed = true;
  await app.close();
  await db.close();
}
process.once("SIGTERM", () => { void close().then(() => process.exit(0)); });
process.once("SIGINT", () => { void close().then(() => process.exit(0)); });

try {
  const registered = await registerUser(db, { email: "workgraph-contract@example.test", name: "Workgraph contract", password: "isolated-test-password" });
  const countryId = registered.user.countryId;
  const service = new AppService(db);
  const city = await service.createCity(countryId, { name: "Contract city", idempotencyKey: "workgraph-contract-city" });
  const district = await service.createDistrict(countryId, { cityId: city.id, name: "Contract district", activate: true, idempotencyKey: "workgraph-contract-district" });
  const task = await service.createTask(countryId, { cityId: city.id, districtId: district.id, title: "Read and publish through real MCP", estimate: 1, idempotencyKey: "workgraph-contract-task" });
  await service.replaceTaskChecklist(countryId, { taskId: task.id, items: [{ title: "FixtureChecklistMarker", done: false }], idempotencyKey: "context-checklist" });
  await service.addTaskComment(countryId, { taskId: task.id, body: "FixtureCommentMarker DB_PASSWORD=context-comment-secret", idempotencyKey: "context-comment" });
  await service.createTaskDefect(countryId, { taskId: task.id, title: "FixtureDefectMarker", reproductionSteps: "Use an expired token", actualResult: "Accepted", expectedResult: "Rejected", idempotencyKey: "context-defect" });
  const dependency = await service.createTask(countryId, { cityId: city.id, districtId: district.id, title: "FixtureDependencyMarker", estimate: 1, idempotencyKey: "context-dependency-task" });
  await service.addTaskDependency(countryId, { taskId: task.id, dependsOnTaskId: dependency.id, idempotencyKey: "context-dependency" });
  const token = await createMcpToken(db, countryId, "Isolated Workgraph", registered.user.id, { scopes: ["country:read", "tasks:read", "tasks:write"] });
  const mcp = registerMcpHttp(app, db, service, process.env.APP_ORIGIN);
  app.get("/__test/state", async () => {
    const result = await service.getTask(countryId, task.id);
    const writes = await db.prepare("SELECT count(*)::integer AS count FROM idempotency WHERE country_id=? AND operation='task.document.upsert.v1'").get(countryId);
    const linkWrites = await db.prepare("SELECT count(*)::integer AS count FROM idempotency WHERE country_id=? AND operation='task.link.add.v1'").get(countryId);
    return { task: result, writes: writes?.count, linkWrites: linkWrites?.count };
  });
  app.post("/__test/revoke", async () => {
    await db.prepare("UPDATE mcp_tokens SET revoked_at=? WHERE id=?").run(new Date().toISOString(),token.id);
    return { revoked: true };
  });
  await app.listen({ host: "127.0.0.1", port: Number(new URL(process.env.APP_ORIGIN).port) });
  console.log(JSON.stringify({ country_id: countryId, task_id: task.id, token: token.token }));
  process.once("exit", () => { void mcp.close(); });
} catch (error) {
  await close();
  throw error;
}
