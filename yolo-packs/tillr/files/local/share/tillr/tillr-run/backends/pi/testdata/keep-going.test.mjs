import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import keepGoing, { decide, isReadOnlyBrief } from "../keep-going.ts";

test("call-suspends settles complete obstacles and nudges plain or incomplete stops", async () => {
	process.env.TILLR_GOAL_BASE = "base";
	process.env.TILLR_CALL_SUSPENDS = "1";
	const handlers = {};
	keepGoing({
		on(name, fn) { handlers[name] = fn; },
		async exec(_cmd, args) {
			return { code: 0, stdout: args[1] === "git rev-parse HEAD" ? "base\n" : "" };
		},
	});
	async function end(content) {
		handlers.agent_end({ messages: [{ role: "assistant", content, stopReason: "stop" }] });
		return handlers.agent_before_settle({ entries: [], outcome: "completed" });
	}
	assert.equal(await end("OBSTACLE: owner\nQUESTION: Which option?\nRESULT: blocked"), undefined);
	assert.match((await end("OBSTACLE: owner\nRESULT: blocked")).entries[0].content, /QUESTION/);
	assert.match((await end("OBSTACLE: seat\nTRIED: Read files\nMISSING: Implementation\nRESULT: blocked")).entries[0].content, /unchanged from the base/);
	assert.match((await end("RESULT: done")).entries[0].content, /Call `land`/);
});

test("a planned write piece can report verified completion at a clean, unchanged HEAD", async (t) => {
	const dir = mkdtempSync(join(tmpdir(), "keep-going-piece-"));
	t.after(() => rmSync(dir, { recursive: true, force: true }));
	const brief = join(dir, "brief.md");
	const section = "## Workflow node\n\nRun only node work/piece-mirror on the build seat; access: write.\n" +
		"An unchanged piece may finish without a commit.\n";
	const report = "Verified: pack sync left the mirror unchanged.\n\nRESULT: done";
	const run = async ({ calls, body = section, status = "", head = "piece-base", text = report }) => {
		writeFileSync(brief, body);
		process.env.BOARD_DRAIN_BRIEF = brief;
		process.env.TILLR_GOAL_BASE = "piece-base";
		delete process.env.TILLR_READ_ONLY_GROUP;
		process.env.TILLR_CALL_SUSPENDS = calls ? "1" : "";
		const handlers = {};
		keepGoing({
			on(name, fn) { handlers[name] = fn; },
			async exec(_cmd, args) {
				const stdout = args[1] === "git rev-parse HEAD" ? head : args[1].startsWith("git status") ? status : "";
				return { code: 0, stdout };
			},
		});
		handlers.agent_end({ messages: [{ role: "assistant", content: text, stopReason: "stop" }] });
		return (await handlers.agent_before_settle({ entries: [], outcome: "completed" }))?.continue;
	};
	const env = { ...process.env };
	t.after(() => { process.env = env; });
	for (const calls of [false, true]) {
		assert.equal(await run({ calls }), undefined);
		assert.equal(await run({ calls, status: "?? scratch\n" }), true);
		assert.equal(await run({ calls, text: "Still working" }), true);
		assert.equal(await run({ calls, body: "Brief\n## Saved request words (verbatim; 123 bytes)\n" + section }), true);
	}
	assert.equal(await run({ calls: true, head: "new-commit" }), true);
});

test("read-only step may stop without a commit while a writing step still may not", async (t) => {
	const dir = mkdtempSync(join(tmpdir(), "keep-going-readonly-"));
	t.after(() => rmSync(dir, { recursive: true, force: true }));
	const planBrief = join(dir, "plan-brief.md");
	writeFileSync(planBrief, [
		"## Workflow node",
		"",
		"Run only node plan on the plan seat; access: read_only.",
		"Read only: do not change files in the target or commit.",
	].join("\n"));
	const writeBrief = join(dir, "write-brief.md");
	writeFileSync(writeBrief, [
		"## Workflow node",
		"",
		"Run only node work on the build seat; access: write.",
	].join("\n"));

	assert.equal(isReadOnlyBrief(planBrief), true);
	assert.equal(isReadOnlyBrief(writeBrief), false);

	const tree = { head: "base123456789", status: "", content: "hash" };
	const readOnlyDone = decide({
		base: "base123456789",
		outcome: "completed",
		stopReason: "stop",
		text: "Plan summary.\n\nRESULT: done",
		tree,
		previous: undefined,
		paused: false,
		readOnly: true,
	});
	assert.equal(readOnlyDone.action, "settle");

	const writeDone = decide({
		base: "base123456789",
		outcome: "completed",
		stopReason: "stop",
		text: "Finished.\n\nRESULT: done",
		tree,
		previous: undefined,
		paused: false,
		readOnly: false,
	});
	assert.equal(writeDone.action, "continue");
	assert.match(writeDone.message, /there is no commit/);

	const oldEnv = {
		TILLR_GOAL_BASE: process.env.TILLR_GOAL_BASE,
		TILLR_CALL_SUSPENDS: process.env.TILLR_CALL_SUSPENDS,
		TILLR_READ_ONLY_GROUP: process.env.TILLR_READ_ONLY_GROUP,
		BOARD_DRAIN_BRIEF: process.env.BOARD_DRAIN_BRIEF,
	};
	t.after(() => {
		for (const [k, v] of Object.entries(oldEnv)) {
			if (v === undefined) delete process.env[k];
			else process.env[k] = v;
		}
	});

	delete process.env.TILLR_CALL_SUSPENDS;
	delete process.env.TILLR_READ_ONLY_GROUP;
	process.env.TILLR_GOAL_BASE = "base123456789";

	const runStep = async (briefPath, text) => {
		process.env.BOARD_DRAIN_BRIEF = briefPath;
		const handlers = {};
		keepGoing({
			on(name, fn) { handlers[name] = fn; },
			async exec(_cmd, args) {
				if (args[1] === "git rev-parse HEAD") return { code: 0, stdout: "base123456789\n" };
				return { code: 0, stdout: "" };
			},
		});
		handlers.agent_end({ messages: [{ role: "assistant", content: text, stopReason: "stop" }] });
		return handlers.agent_before_settle({ entries: [], outcome: "completed" });
	};

	const planResult = await runStep(planBrief, "Plan summary.\n\nRESULT: done");
	assert.equal(planResult.continue, undefined);
	assert.equal(planResult.entries.at(-1).data.action, "settle");

	const writeResult = await runStep(writeBrief, "Finished.\n\nRESULT: done");
	assert.equal(writeResult.continue, true);
	assert.match(writeResult.entries.at(-1).content, /there is no commit/);
});

test("committed step with unformatted Go files is refused back to the builder with the file names until formatted", async (t) => {
	const { copyFileSync, mkdirSync } = await import("node:fs");
	const { spawnSync } = await import("node:child_process");
	const { fileURLToPath } = await import("node:url");
	const repoRoot = fileURLToPath(new URL("../../../../../", import.meta.url));
	const dir = mkdtempSync(join(tmpdir(), "keep-going-format-"));
	const prevCwd = process.cwd();
	const oldEnv = { ...process.env };
	t.after(() => {
		process.chdir(prevCwd);
		for (const k of Object.keys(process.env)) if (!(k in oldEnv)) delete process.env[k];
		Object.assign(process.env, oldEnv);
		rmSync(dir, { recursive: true, force: true });
	});

	const sh = (script) => {
		const r = spawnSync("sh", ["-c", script], {
			cwd: dir,
			encoding: "utf8",
			env: { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@t", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@t" },
		});
		assert.equal(r.status, 0, r.stderr || r.stdout);
		return r.stdout.trim();
	};
	sh("git init -q");
	copyFileSync(join(repoRoot, ".justfile"), join(dir, ".justfile"));
	mkdirSync(join(dir, "tools/gate/checks"), { recursive: true });
	copyFileSync(join(repoRoot, "tools/gate/checks/format.json"), join(dir, "tools/gate/checks/format.json"));
	sh("git add .justfile tools/gate/checks/format.json && git commit -qm base");
	const base = sh("git rev-parse HEAD");

	mkdirSync(join(dir, "tools/example"), { recursive: true });
	writeFileSync(join(dir, "tools/example/new_file.go"), "package example\nfunc bad(){println(1)}\n");
	sh("git add tools/example/new_file.go && git commit -qm unformatted");

	process.chdir(dir);
	delete process.env.TILLR_CALL_SUSPENDS;
	delete process.env.TILLR_READ_ONLY_GROUP;
	delete process.env.BOARD_DRAIN_BRIEF;
	process.env.TILLR_GOAL_BASE = base;

	const handlers = {};
	keepGoing({
		on(name, fn) { handlers[name] = fn; },
		async exec(cmd, args) {
			const r = spawnSync(cmd, args, { cwd: dir, encoding: "utf8" });
			return { code: r.status, stdout: r.stdout, stderr: r.stderr };
		},
	});
	const end = async (text) => {
		handlers.agent_end({ messages: [{ role: "assistant", content: text, stopReason: "stop" }] });
		return handlers.agent_before_settle({ entries: [], outcome: "completed" });
	};

	const refused = await end("RESULT: done");
	assert.equal(refused.continue, true);
	assert.match(refused.entries.at(-1).content, /tools\/example\/new_file\.go/);

	sh("just format && git add tools/example/new_file.go && git commit -q --amend --no-edit");
	const settled = await end("RESULT: done");
	assert.equal(settled.continue, undefined);
	assert.equal(settled.entries.at(-1).data.action, "settle");
});
