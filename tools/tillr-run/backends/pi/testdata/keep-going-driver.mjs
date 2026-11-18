// Drives keep-going.ts the way pi does, without a model: a fake `pi` that records the
// handlers the extension registers, runs `pi.exec` for real in the cwd (a git repository
// the Go test made), and fires agent_end then agent_before_settle for each scripted ending.
// Prints one JSON object: each step's name mapped to what the extension answered.
import { spawnSync } from "node:child_process";
import { rmSync, writeFileSync } from "node:fs";

const extension = (await import(process.argv[2])).default;

function fakePi() {
	const handlers = {};
	return {
		handlers,
		on(name, fn) {
			handlers[name] = fn;
		},
		async exec(cmd, args) {
			const r = spawnSync(cmd, args, { encoding: "utf8" });
			return { stdout: r.stdout, stderr: r.stderr, code: r.status };
		},
	};
}

const pi = fakePi();
extension(pi);

async function end(text, stopReason = "stop", outcome = "completed") {
	pi.handlers.agent_end({ messages: [{ role: "assistant", content: [{ type: "text", text }], stopReason }] });
	const result = await pi.handlers.agent_before_settle({ entries: [], continue: false, outcome });
	const entry = result?.entries?.at(-1);
	return {
		continue: result?.continue === true,
		type: entry?.type,
		customType: entry?.customType,
		message: entry?.content ?? "",
		reason: entry?.data?.reason ?? "",
	};
}

const sh = (script) => spawnSync("sh", ["-c", script], { encoding: "utf8" });
const out = {};
const quit = "1. **Paths changed:** None.\n\nRESULT: failed";
out.quit = await end(quit);
out.reworded = await end("1. **Paths changed:** None. I read two more files.\n\nRESULT: failed");
out.repeat = await end("1. **Paths changed:** None. I read two more files.\n\nRESULT: failed");
writeFileSync("a.txt", "work in progress\n");
out.edited = await end(quit);
out.doneUncommitted = await end("Finished.\n\nRESULT: done");
out.blocked = await end("4. **Blocker:** the design contradicts criterion 2.\n\nRESULT: blocked");
out.error = await end("", "error");
out.aborted = await end(quit, "stop", "aborted");
writeFileSync(process.env.TILLR_PAUSE_REQUEST, "");
out.paused = await end(quit);
rmSync(process.env.TILLR_PAUSE_REQUEST);
sh("git add a.txt && git commit -q -m work");
out.committedNoFooter = await end("I committed the change.");
out.goal = await end("1. **Paths changed:** a.txt\n\nRESULT: done");
writeFileSync("b.txt", "left behind\n");
out.committedDirty = await end("1. **Paths changed:** a.txt\n\nRESULT: done");

delete process.env.TILLR_GOAL_BASE;
const idle = fakePi();
extension(idle);
out.noGoalHandlers = Object.keys(idle.handlers).length;

console.log(JSON.stringify(out));
