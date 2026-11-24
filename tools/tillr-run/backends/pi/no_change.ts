import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

function readRecord(path: string) {
	try { return JSON.parse(readFileSync(path, "utf8")); }
	catch (error: any) { if (error.code !== "ENOENT") throw error; return undefined; }
}

const park = { content: [], details: {}, park: true };

export interface NoChangeArgs { cause?: string; evidence?: string; why_no_change?: string; }

export function validateNoChangeArgs(args: NoChangeArgs): { cause: string; evidence: string; why_no_change: string } {
	const cause = typeof args?.cause === "string" ? args.cause.trim() : "";
	const evidence = typeof args?.evidence === "string" ? args.evidence.trim() : "";
	const whyNoChange = typeof args?.why_no_change === "string" ? args.why_no_change.trim() : "";
	const missing = [["cause", cause], ["evidence", evidence], ["why_no_change", whyNoChange]].filter(([, v]) => !v).map(([k]) => k);
	if (missing.length) throw new Error(`no_change is missing required field(s): ${missing.join(", ")}.`);
	return { cause, evidence, why_no_change: whyNoChange };
}

export function executeNoChange(
	callId: string, args: NoChangeArgs, cwd: string, durable: string, base: string,
	owedWakes: () => string[], answered: (id: string) => boolean = () => false, transcript = "",
) {
	if (!durable || !base) throw new Error("The run's scratch or base is not staged.");
	const answer = readRecord(join(durable, "landing-answer.json"));
	if (answer !== undefined && answer?.call_id === callId) {
		if (typeof answer.text !== "string" || !answer.text.trim()) throw new Error("The no_change answer has no text.");
		return { content: [{ type: "text", text: answer.text }], details: answer, isError: true };
	}
	const path = join(durable, "landing-offer.json");
	const offer = readRecord(path);
	if (offer?.call_id === callId) return park;
	if (offer !== undefined && typeof offer?.call_id === "string" && answered(offer.call_id)) unlinkSync(path);
	else if (offer !== undefined) throw new Error(`Another ending call is open: ${offer?.call_id}.`);
	const state = readRecord(join(durable, "landing-state.json"));
	const open = state?.open?.find((entry: { call_id: string }) => entry.call_id !== callId && !answered(entry.call_id));
	if (open) throw new Error(`Another ending call is open: ${open.call_id}.`);
	if (state?.open?.some((entry: { call_id: string }) => entry.call_id === callId)) return park;
	if (answer !== undefined && !answered(answer?.call_id)) throw new Error("The staged landing answer belongs to a different call.");

	const wakes = owedWakes();
	if (wakes.length) throw new Error(`Wait for these background task wakes before calling no_change: ${wakes.join(", ")}.`);

	const finding = validateNoChangeArgs(args);
	const git = (...gitArgs: string[]) => execFileSync("git", gitArgs, { cwd, encoding: "utf8" });
	if (git("rev-parse", "--verify", "HEAD").trim() !== base) {
		throw new Error("A commit exists at HEAD (HEAD is not the staged base); call `land` instead.");
	}
	const status = git("status", "--porcelain=v1", "--untracked-files=all").trim();
	if (status.length > 0) {
		throw new Error(`The worktree is not clean; commit and call \`land\`, or remove uncommitted paths before calling no_change:\n${status}`);
	}
	const refusal = state?.refused?.find((e: any) =>
		e.tool === "no_change" && e.base === base &&
		typeof e.finding?.cause === "string" && e.finding.cause.trim() === finding.cause &&
		typeof e.finding?.evidence === "string" && e.finding.evidence.trim() === finding.evidence &&
		typeof e.finding?.why_no_change === "string" && e.finding.why_no_change.trim() === finding.why_no_change,
	);
	if (refusal) {
		throw new Error(`This no_change finding was already refused. Provide new evidence or make the required change.\n${refusal.answer ?? ""}`);
	}
	const temp = `${path}.${randomUUID()}`;
	try {
		writeFileSync(temp, JSON.stringify({ call_id: callId, tool: "no_change", base, finding, transcript }) + "\n", { mode: 0o600, flag: "wx" });
		renameSync(temp, path);
	} finally {
		try { unlinkSync(temp); } catch (error: any) { if (error.code !== "ENOENT") throw error; }
	}
	return park;
}

export default function noChange(pi: any) {
	const since = Math.floor(Date.now() - process.uptime() * 1000) - 1000;
	pi.registerTool({
		name: "no_change",
		label: "No Change",
		description: "Report a reproduced finding that nothing in the repository needs changing, ending this run.",
		parameters: {
			type: "object",
			required: ["cause", "evidence", "why_no_change"],
			properties: { cause: { type: "string" }, evidence: { type: "string" }, why_no_change: { type: "string" } },
			additionalProperties: false,
		},
		async execute(callId: string, args: NoChangeArgs, _signal: unknown, _update: unknown, ctx: any) {
			const { YOLO_DURABLE_DIR, TILLR_GOAL_BASE } = process.env;
			if (!YOLO_DURABLE_DIR || !TILLR_GOAL_BASE) throw new Error("The run's scratch or base is not staged.");
			return executeNoChange(callId, args, ctx.cwd, YOLO_DURABLE_DIR, TILLR_GOAL_BASE, () => {
				const raw = execFileSync("python3", [fileURLToPath(new URL("./stop-at-gap", import.meta.url)), "--owed-wakes", String(process.pid), String(since)], { encoding: "utf8" });
				const wakes = JSON.parse(raw);
				if (!Array.isArray(wakes)) throw new Error("Cannot determine whether this Pi session owes a background task wake; check its journal before calling no_change.");
				return wakes;
			}, (id) => ctx.sessionManager.getBranch().some((entry: any) =>
				entry.type === "message" && entry.message?.role === "toolResult" && entry.message.toolCallId === id), ctx.sessionManager.getSessionFile() ?? "");
		},
	});
}
