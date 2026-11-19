import assert from "node:assert/strict";
import test from "node:test";
import keepGoing from "../keep-going.ts";

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
