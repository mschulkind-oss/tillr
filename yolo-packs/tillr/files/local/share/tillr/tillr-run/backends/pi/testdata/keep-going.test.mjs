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
