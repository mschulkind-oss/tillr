import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

function readRecord(path: string) {
	try {
		return JSON.parse(readFileSync(path, "utf8"));
	} catch (error: any) {
		if (error.code !== "ENOENT") throw error;
		return undefined;
	}
}

const park = { content: [], details: {}, park: true };

export function executeLand(
	callId: string, summary: string, cwd: string, durable: string, base: string, fenceFile: string,
	owedWakes: () => string[], answered: (id: string) => boolean = () => false, transcript = "",
) {
	if (!durable || !base || !fenceFile) throw new Error("The run's scratch, base or fence is not staged.");
	const answer = readRecord(join(durable, "landing-answer.json"));
	if (answer !== undefined && answer?.call_id === callId) {
		if (typeof answer.text !== "string" || !answer.text.trim()) throw new Error("The landing answer has no text.");
		return { content: [{ type: "text", text: answer.text }], details: answer, isError: true };
	}
	const path = join(durable, "landing-offer.json");
	const offer = readRecord(path);
	if (offer?.call_id === callId) return park;
	// A parked call never gets a tool result, so an offer whose call already has one never
	// parked: pi failed it after the write. Leaving it would refuse every later ending call.
	if (offer !== undefined && typeof offer?.call_id === "string" && answered(offer.call_id)) unlinkSync(path);
	else if (offer !== undefined) throw new Error(`Another land call is open: ${offer?.call_id}.`);
	const state = readRecord(join(durable, "landing-state.json"));
	const open = state?.open?.find((entry: { call_id: string }) => entry.call_id !== callId && !answered(entry.call_id));
	if (open) throw new Error(`Another land call is open: ${open.call_id}.`);
	if (state?.open?.some((entry: { call_id: string }) => entry.call_id === callId)) return park;
	if (answer !== undefined && !answered(answer?.call_id)) throw new Error("The staged landing answer belongs to a different call.");
	if (typeof summary !== "string" || !summary.trim() || [...summary].length > 2000) {
		throw new Error("Provide a nonempty summary of at most 2,000 characters.");
	}
	const git = (...args: string[]) => execFileSync("git", args, { cwd, encoding: "utf8" });
	const commit = git("rev-parse", "--verify", "HEAD").trim();
	if (commit === base) throw new Error("There is no commit to offer: HEAD is still the staged base.");
	const fence = readFileSync(fenceFile, "utf8").split(/\r?\n/).map((line) => line.trim().replace(/\/+$/, "")).filter(Boolean);
	if (!fence.length) throw new Error("The staged fence is empty.");
	const changed = new Set([
		...git("diff", "--name-only", "--no-renames", "-z").split("\0"),
		...git("diff", "--cached", "--name-only", "--no-renames", "-z").split("\0"),
		...git("ls-files", "--others", "--exclude-standard", "-z").split("\0"),
	]);
	const dirty = [...changed].filter((name) => name && fence.some((prefix) => name === prefix || name.startsWith(`${prefix}/`)));
	if (dirty.length) throw new Error(`Commit or remove these uncommitted paths inside the fence:\n${dirty.join("\n")}`);
	const refused = state?.refused?.find((entry: { commit: string }) => entry.commit === commit);
	if (refused) throw new Error(`This commit was already refused. A new commit is required.\n${refused.answer}`);
	const wakes = owedWakes();
	if (wakes.length) throw new Error(`Wait for these background task wakes before calling land: ${wakes.join(", ")}.`);

	const temp = `${path}.${randomUUID()}`;
	try {
		writeFileSync(temp, JSON.stringify({ call_id: callId, commit, base, summary, transcript }) + "\n", { mode: 0o600, flag: "wx" });
		renameSync(temp, path);
	} finally {
		try { unlinkSync(temp); } catch (error: any) { if (error.code !== "ENOENT") throw error; }
	}
	return park;
}

export default function land(pi: any) {
	const since = Math.floor(Date.now() - process.uptime() * 1000) - 1000;
	pi.registerTool({
		name: "land",
		label: "Land",
		description: "Offer the commit at HEAD for landing, ending this run. Include what changed and which verification ran.",
		parameters: { type: "object", required: ["summary"], properties: { summary: { type: "string", maxLength: 2000 } }, additionalProperties: false },
		// Pi settles execute's value with `.then`, so it must be a promise; async also turns a
		// throw into a rejection.
		async execute(callId: string, args: { summary: string }, _signal: unknown, _update: unknown, ctx: { cwd: string; sessionManager: { getSessionFile(): string | undefined; getBranch(): { type: string; message?: { role: string; toolCallId?: string } }[] } }) {
			const { YOLO_DURABLE_DIR, TILLR_GOAL_BASE, BOARD_DRAIN_BRIEF } = process.env;
			if (!YOLO_DURABLE_DIR || !TILLR_GOAL_BASE || !BOARD_DRAIN_BRIEF) throw new Error("The run's scratch, base or brief is not staged.");
			return executeLand(callId, args.summary, ctx.cwd, YOLO_DURABLE_DIR, TILLR_GOAL_BASE,
				join(dirname(BOARD_DRAIN_BRIEF), "fence.txt"), () => {
					const raw = execFileSync("python3", [fileURLToPath(new URL("./stop-at-gap", import.meta.url)), "--owed-wakes", String(process.pid), String(since)], { encoding: "utf8" });
					const wakes = JSON.parse(raw);
					if (!Array.isArray(wakes)) throw new Error("Cannot determine whether this Pi session owes a background task wake; check its journal before offering.");
					return wakes;
				}, (id) => ctx.sessionManager.getBranch().some((entry) =>
					entry.type === "message" && entry.message?.role === "toolResult" && entry.message.toolCallId === id), ctx.sessionManager.getSessionFile() ?? "");
		},
	});
}
