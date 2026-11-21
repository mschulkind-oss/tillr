import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { fencePathsProblem, OBSTACLE_REQUIRED, SEAT_NEEDS_ATTEMPT } from "./keep-going.ts";

function readRecord(path: string) {
	try {
		return JSON.parse(readFileSync(path, "utf8"));
	} catch (error: any) {
		if (error.code !== "ENOENT") throw error;
		return undefined;
	}
}

const park = { content: [], details: {}, park: true };

export interface ObstacleArgs {
	kind: string;
	criteria?: string;
	evidence?: string;
	paths?: string;
	checks?: string;
	base?: string;
	command?: string;
	tried?: string;
	missing?: string;
	question?: string;
}

export function validateObstacleArgs(args: ObstacleArgs): { kind: string; fields: Record<string, string> } {
	const kind = typeof args?.kind === "string" ? args.kind.trim().toLowerCase() : "";
	const kinds = Object.keys(OBSTACLE_REQUIRED).sort();
	const required = OBSTACLE_REQUIRED[kind];
	if (!required) {
		throw new Error(`Invalid obstacle kind ${JSON.stringify(args?.kind ?? "")}; expected one of ${kinds.join(", ")}.`);
	}
	const fields: Record<string, string> = {};
	const missing: string[] = [];
	for (const name of required) {
		const raw = (args as Record<string, unknown>)[name.toLowerCase()];
		const value = typeof raw === "string" ? raw.trim() : "";
		if (!value) missing.push(name.toLowerCase());
		else fields[name] = value;
	}
	if (missing.length) {
		throw new Error(`The ${kind} obstacle is missing required field(s): ${missing.join(", ")}.`);
	}
	if (kind === "fence") {
		const problem = fencePathsProblem(fields.PATHS);
		if (problem) throw new Error(`${problem}.`);
	}
	if (kind === "tooling") {
		const entries = fields.PATHS.split(/[\s,]+/).map((p) => p.replace(/^[`*-]+|[`*-]+$/g, "")).filter(Boolean);
		if (!entries.length) {
			throw new Error(`The ${kind} obstacle is missing required field(s): paths.`);
		}
		for (const path of entries) {
			if (path.startsWith("/") || path === ".." || path.startsWith("../")) {
				throw new Error(`The ${kind} obstacle's paths must be repository-relative, and ${path} is not.`);
			}
		}
	}
	return { kind, fields };
}

export function defaultAttempted(cwd: string, base: string): boolean {
	const git = (...args: string[]) => execFileSync("git", args, { cwd, encoding: "utf8" });
	const head = git("rev-parse", "--verify", "HEAD").trim();
	if (head !== base) return true;
	const status = git("status", "--porcelain=v1", "--untracked-files=all").trim();
	return status.length > 0;
}

export function executeObstacle(
	callId: string, args: ObstacleArgs, cwd: string, durable: string, base: string,
	owedWakes: () => string[], answered: (id: string) => boolean = () => false, transcript = "",
	attempted: () => boolean = () => defaultAttempted(cwd, base),
) {
	if (!durable || !base) throw new Error("The run's scratch or base is not staged.");
	const answer = readRecord(join(durable, "landing-answer.json"));
	if (answer !== undefined && answer?.call_id === callId) {
		if (typeof answer.text !== "string" || !answer.text.trim()) throw new Error("The obstacle answer has no text.");
		return { content: [{ type: "text", text: answer.text }], details: answer, isError: true };
	}
	const path = join(durable, "landing-offer.json");
	const offer = readRecord(path);
	if (offer?.call_id === callId) return park;
	// A parked call never gets a tool result, so an offer whose call already has one never
	// parked: pi failed it after the write. Leaving it would refuse every later ending call.
	if (offer !== undefined && typeof offer?.call_id === "string" && answered(offer.call_id)) unlinkSync(path);
	else if (offer !== undefined) throw new Error(`Another ending call is open: ${offer?.call_id}.`);
	const state = readRecord(join(durable, "landing-state.json"));
	const open = state?.open?.find((entry: { call_id: string }) => entry.call_id !== callId && !answered(entry.call_id));
	if (open) throw new Error(`Another ending call is open: ${open.call_id}.`);
	if (state?.open?.some((entry: { call_id: string }) => entry.call_id === callId)) return park;
	if (answer !== undefined && !answered(answer?.call_id)) throw new Error("The staged landing answer belongs to a different call.");

	const obstacle = validateObstacleArgs(args);
	if (obstacle.kind === "seat" && !attempted()) {
		throw new Error(SEAT_NEEDS_ATTEMPT);
	}
	const wakes = owedWakes();
	if (wakes.length) throw new Error(`Wait for these background task wakes before calling obstacle: ${wakes.join(", ")}.`);

	const temp = `${path}.${randomUUID()}`;
	try {
		writeFileSync(temp, JSON.stringify({ call_id: callId, tool: "obstacle", base, obstacle, transcript }) + "\n", { mode: 0o600, flag: "wx" });
		renameSync(temp, path);
	} finally {
		try { unlinkSync(temp); } catch (error: any) { if (error.code !== "ENOENT") throw error; }
	}
	return park;
}

export default function obstacle(pi: any) {
	const since = Math.floor(Date.now() - process.uptime() * 1000) - 1000;
	pi.registerTool({
		name: "obstacle",
		label: "Obstacle",
		description: "Report an obstacle that prevents finishing this run, ending the run until resolved. Provide kind and that kind's required fields.",
		parameters: {
			type: "object",
			required: ["kind"],
			properties: {
				kind: { type: "string", enum: Object.keys(OBSTACLE_REQUIRED).sort() },
				criteria: { type: "string" },
				evidence: { type: "string" },
				paths: { type: "string", description: "For a fence obstacle, list repository-relative paths separated by commas or newlines; no prose, Git refs, or placeholders." },
				checks: { type: "string" },
				base: { type: "string" },
				command: { type: "string" },
				tried: { type: "string" },
				missing: { type: "string" },
				question: { type: "string" },
			},
			additionalProperties: false,
		},
		// Pi settles execute's value with `.then`, so it must be a promise; async also turns a
		// throw into a rejection.
		async execute(callId: string, args: ObstacleArgs, _signal: unknown, _update: unknown, ctx: { cwd: string; sessionManager: { getSessionFile(): string | undefined; getBranch(): { type: string; message?: { role: string; toolCallId?: string } }[] } }) {
			const { YOLO_DURABLE_DIR, TILLR_GOAL_BASE } = process.env;
			if (!YOLO_DURABLE_DIR || !TILLR_GOAL_BASE) throw new Error("The run's scratch or base is not staged.");
			return executeObstacle(callId, args, ctx.cwd, YOLO_DURABLE_DIR, TILLR_GOAL_BASE, () => {
				const raw = execFileSync("python3", [fileURLToPath(new URL("./stop-at-gap", import.meta.url)), "--owed-wakes", String(process.pid), String(since)], { encoding: "utf8" });
				const wakes = JSON.parse(raw);
				if (!Array.isArray(wakes)) throw new Error("Cannot determine whether this Pi session owes a background task wake; check its journal before reporting an obstacle.");
				return wakes;
			}, (id) => ctx.sessionManager.getBranch().some((entry) =>
				entry.type === "message" && entry.message?.role === "toolResult" && entry.message.toolCallId === id), ctx.sessionManager.getSessionFile() ?? "");
		},
	});
}
