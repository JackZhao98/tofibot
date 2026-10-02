import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-occurrence-checks-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/scheduleOccurrences.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--declaration", "false", "--pretty", "false"], { cwd: root });
  const compiled = join(output, "scheduleOccurrences.js");
  await writeFile(compiled, (await readFile(compiled, "utf8")).replace('from "./timezone"', 'from "./timezone.js"'));
  const { occurrenceRootKey, loadScheduleOccurrences, hasActiveOccurrence } = await import(pathToFileURL(compiled));
  const id = n => `00000000-0000-0000-0000-${String(n).padStart(12, "0")}`;
  const item = (root, status = "done", extra = {}) => ({ root_run_id: root, schedule_id: id(1000), status_run_id: id(1001), execution_status: status, scheduled_for_utc: "2026-09-22T00:00:00Z", result_in_conversation: false, ...extra });
  assert.equal(occurrenceRootKey([id(2), "", "bad", id(1), id(2)]), `${id(1)},${id(2)}`);
  const signal = new AbortController().signal;
  let calls = 0;
  const roots = Array.from({ length: 102 }, (_, i) => id(i + 1));
  const batched = await loadScheduleOccurrences("conversation/one", occurrenceRootKey(roots), signal, async (path, init) => {
    assert.equal(init.signal, signal);
    assert.match(path, /^\/api\/conversations\/conversation%2Fone\/schedule-occurrences\?/);
    const ids = new URL(path, "http://fixture.invalid").searchParams.get("root_run_ids").split(",");
    assert.ok(ids.length <= 50);
    calls += 1;
    return { occurrences: ids.map(root => item(root)) };
  });
  assert.equal(calls, 3);
  assert.equal(batched.size, 102);
  assert.equal(hasActiveOccurrence(batched), false);
  const exact = await loadScheduleOccurrences("c", occurrenceRootKey([id(1), id(2), id(3)]), signal, async () => ({ occurrences: [item(id(1), "failed"), item(id(2), "running"), item(id(3), "done", { status_run_id: "invalid" }), item(id(99), "done")] }));
  assert.deepEqual([...exact.keys()], [id(1), id(2)]);
  assert.equal(exact.get(id(1)).execution_status, "failed");
  assert.equal(hasActiveOccurrence(exact), true);
  const diagnostic = await loadScheduleOccurrences("c", occurrenceRootKey([id(1), id(2), id(3), id(4), id(5)]), signal, async () => ({ occurrences: [
    item(id(1), "failed", { status_error: "Delegate unavailable\n<plain text>" }),
    item(id(2), "done", { status_error: "STALE_ERROR" }),
    item(id(3), "failed", { status_error: { unexpected: true } }),
    item(id(4), "interrupted", { status_error: "🙂".repeat(3000) }),
    item(id(5), "running", { status_error: "STALE_ERROR" }),
    item(id(99), "failed", { status_error: "FOREIGN_ERROR" }),
  ] }));
  assert.equal(diagnostic.get(id(1)).status_error, "Delegate unavailable\n<plain text>");
  assert.equal(diagnostic.get(id(2)).status_error, undefined);
  assert.equal(diagnostic.get(id(3)).status_error, undefined);
  assert.equal(Array.from(diagnostic.get(id(4)).status_error).length, 2400);
  assert.equal(diagnostic.get(id(5)).status_error, undefined);
  const publication = await loadScheduleOccurrences("c", occurrenceRootKey([id(1), id(2), id(3)]), signal, async () => ({ occurrences: [
    item(id(1), "done", { result_in_conversation: true }),
    item(id(2), "running", { result_in_conversation: true }),
    item(id(3), "done", { result_in_conversation: "true" }),
  ] }));
  assert.equal(publication.get(id(1)).result_in_conversation, true);
  assert.equal(publication.get(id(2)).result_in_conversation, false);
  assert.equal(publication.get(id(3)).result_in_conversation, false);
  assert.equal(diagnostic.has(id(99)), false);
  assert.equal((await loadScheduleOccurrences("c", id(1), signal, async () => ({ occurrences: [] }))).size, 0);
  await assert.rejects(loadScheduleOccurrences("c", id(1), signal, async () => ({ occurrences: null })), /Invalid/);
  await assert.rejects(loadScheduleOccurrences("c", id(1), signal, async () => { throw new Error("offline"); }), /offline/);
  const aborted = new AbortController();
  aborted.abort();
  let invoked = false;
  await assert.rejects(loadScheduleOccurrences("c", id(1), aborted.signal, async () => { invoked = true; return {}; }), { name: "AbortError" });
  assert.equal(invoked, false);
  const late = new AbortController();
  let resolve;
  const oldRead = loadScheduleOccurrences("old", id(1), late.signal, () => new Promise(done => { resolve = done; }));
  late.abort();
  resolve({ occurrences: [item(id(1))] });
  await assert.rejects(oldRead, { name: "AbortError" });
  let partialCalls = 0;
  await assert.rejects(loadScheduleOccurrences("c", occurrenceRootKey(roots), signal, async path => {
    if (++partialCalls === 2) throw new Error("second batch failed");
    const ids = new URL(path, "http://fixture.invalid").searchParams.get("root_run_ids").split(",");
    return { occurrences: ids.map(root => item(root)) };
  }), /second batch failed/);
  console.log("schedule occurrences: PASS (exact roots, batching, invalid/foreign IDs, historical outcomes, bounded failure diagnostics, empty/error, cancellation, atomic batch failure)");
} finally {
  await rm(output, { recursive: true, force: true });
}
