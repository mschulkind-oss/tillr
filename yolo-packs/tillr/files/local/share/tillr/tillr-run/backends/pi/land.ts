import { execFileSync, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

function readRecord(path: string) {
	try {
		return JSON.parse(readFileSync(path, "utf8"));
	} catch (error: any) {
		if (error.code !== "ENOENT") throw error;
		return undefined;
	}
}

function unformattedPaths(cwd: string): string[] {
	const decl = readRecord(join(cwd, "tools/gate/checks/format.json"));
	if (!Array.isArray(decl?.command) || decl.command.length === 0 || !decl.command.every((arg: unknown) => typeof arg === "string" && arg.length > 0)) {
		return [];
	}
	const cmd = decl.command as string[];
	const result = spawnSync(cmd[0], cmd.slice(1), { cwd, encoding: "utf8" });
	if (!result.error && result.status === 0) return [];
	const outLines = String(result.stdout ?? "").split("\n").map((line) => line.trim()).filter(Boolean);
	if (outLines.length > 0) return outLines;
	const errLines = String(result.stderr ?? "").split("\n").map((line) => line.trim()).filter(Boolean);
	if (errLines.length > 0) return errLines;
	return [result.error ? result.error.message : `${cmd.join(" ")} exited ${result.status ?? "non-zero"}`];
}

const park = { content: [], details: {}, park: true };

export function executeLand(
	callId: string, cwd: string, durable: string, base: string,
	owedWakes: () => string[], answered: (id: string) => boolean = () => false, transcript = "",
) {
	if (!durable || !base) throw new Error("The run's scratch or base is not staged.");
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
	const git = (...args: string[]) => execFileSync("git", args, { cwd, encoding: "utf8" });
	const commit = git("rev-parse", "--verify", "HEAD").trim();
	if (commit === base) throw new Error("There is no commit to offer: HEAD is still the staged base.");

	const changed = new Set([
		...git("diff", "--name-only", "--no-renames", "-z").split("\0"),
		...git("diff", "--cached", "--name-only", "--no-renames", "-z").split("\0"),
		...git("ls-files", "--others", "--exclude-standard", "-z").split("\0"),
	]);
	const dirty = [...changed].filter((name) => name);
	if (dirty.length) throw new Error(`Commit or remove these uncommitted paths:\n${dirty.join("\n")}`);
	const unformatted = unformattedPaths(cwd);
	if (unformatted.length) {
		throw new Error(`Format these files (and run \`just pack-sync\` if \`tools/tillr*\` changed), then amend the commit before calling land:\n${unformatted.join("\n")}`);
	}
	const refusal = state?.refused?.find((entry: { commit: string; report_only?: boolean }) =>
		entry.commit === commit && entry.report_only !== true);
	if (refusal) throw new Error(`This commit was already refused. A new commit is required.\n${refusal.answer}`);
	const wakes = owedWakes();
	if (wakes.length) throw new Error(`Wait for these background task wakes before calling land: ${wakes.join(", ")}.`);

	const temp = `${path}.${randomUUID()}`;
	try {
		writeFileSync(temp, JSON.stringify({ call_id: callId, commit, base, transcript }) + "\n", { mode: 0o600, flag: "wx" });
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
		description: "Offer the commit at HEAD for landing, ending this run.",
		parameters: { type: "object", properties: {}, additionalProperties: false },
		// Pi settles execute's value with `.then`, so it must be a promise; async also turns a
		// throw into a rejection.
		async execute(callId: string, _args: Record<string, never>, _signal: unknown, _update: unknown, ctx: { cwd: string; sessionManager: { getSessionFile(): string | undefined; getBranch(): { type: string; message?: { role: string; toolCallId?: string } }[] } }) {
			const { YOLO_DURABLE_DIR, TILLR_GOAL_BASE } = process.env;
			if (!YOLO_DURABLE_DIR || !TILLR_GOAL_BASE) throw new Error("The run's scratch or base is not staged.");
			return executeLand(callId, ctx.cwd, YOLO_DURABLE_DIR, TILLR_GOAL_BASE, () => {
					const raw = execFileSync("python3", [fileURLToPath(new URL("./stop-at-gap", import.meta.url)), "--owed-wakes", String(process.pid), String(since)], { encoding: "utf8" });
					const wakes = JSON.parse(raw);
					if (!Array.isArray(wakes)) throw new Error("Cannot determine whether this Pi session owes a background task wake; check its journal before offering.");
					return wakes;
				}, (id) => ctx.sessionManager.getBranch().some((entry) =>
					entry.type === "message" && entry.message?.role === "toolResult" && entry.message.toolCallId === id), ctx.sessionManager.getSessionFile() ?? "");
		},
	});
}
