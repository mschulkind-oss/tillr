// keep-going: the in-run half of "a run has a goal". run-pi loads this extension into a
// worker whose goal is a commit (a lane worktree run that is not a read-only node) and
// exports the lane's base sha as TILLR_GOAL_BASE. When the session is about to settle, it
// checks the goal against the worktree; if the goal is unmet and the worker named no
// blocker, it answers in the SAME session with what is missing and the run continues.
// Nothing is counted, no new run is started, and nothing is reported as ended.
//
// The only endings it lets settle:
//   - the goal is met: HEAD moved off the base, the worktree is clean, and the last
//     message carries a RESULT footer;
//   - the worker named a complete obstacle: `RESULT: blocked` (or the legacy
//     `RESULT: trunk-broken`) with an `OBSTACLE: <kind>` line and every field that kind
//     requires (the drain's obstacle.go holds the same rule; testdata/obstacle-cases.json
//     is the one set of cases both are tested against). A blocked report without one is
//     a missing output and is answered like any other unmet goal;
//   - an identical repeat: the session ends again with the same tree and the same report
//     (whitespace aside) as when it was last told to keep going, so the nudge changed
//     nothing. A different report on an unchanged tree is not a repeat: a worker that only
//     read files and then quit again is told again;
//   - an ending that is not the model's: an error, an abort, a length stop, or a pause the
//     runner requested (TILLR_PAUSE_REQUEST exists). Those have their own routes.
//
// Why (measured 2026-11-18): Sol 6.2 at medium ended 19 of 61 drain runs with
// "RESULT: failed" and "no blocker established" after one to five read-only commands. In
// an interactive session a human says "continue"; an unattended run had nobody to say it.

import { createHash } from "node:crypto";
import { existsSync } from "node:fs";

export type Footer = "done" | "blocked" | "failed" | "trunk-broken";

export interface Tree {
	head: string;
	status: string;
	content: string;
}

export interface GoalInput {
	base: string;
	outcome: string | undefined;
	stopReason: string | undefined;
	text: string;
	tree: Tree;
	previous: string | undefined;
	paused: boolean;
}

export type GoalDecision =
	| { action: "settle"; reason: string }
	| { action: "continue"; fingerprint: string; message: string };

export function footerOf(text: string): Footer | undefined {
	const lines = text.trim().split("\n");
	const match = /^RESULT: (done|blocked|failed|trunk-broken)$/.exec((lines[lines.length - 1] ?? "").trim());
	return match ? (match[1] as Footer) : undefined;
}

// The closed set of obstacle kinds and each kind's required fields, as obstacle.go has them.
export const OBSTACLE_REQUIRED: Record<string, string[]> = {
	definition: ["CRITERIA", "EVIDENCE"],
	fence: ["PATHS", "EVIDENCE"],
	trunk: ["CHECKS", "BASE"],
	tooling: ["COMMAND", "EVIDENCE", "PATHS"],
	seat: ["TRIED", "MISSING"],
	owner: ["QUESTION"],
};

const FIELDS = new Set(["OBSTACLE", ...Object.values(OBSTACLE_REQUIRED).flat()]);

function fieldLine(line: string): [string, string] | undefined {
	const at = line.indexOf(":");
	if (at < 0) return undefined;
	const name = line.slice(0, at).replace(/^[\s*`-]+|[\s*`-]+$/g, "").toUpperCase();
	if (!FIELDS.has(name)) return undefined;
	return [name, line.slice(at + 1).trim().replace(/^[*`]+|[*`]+$/g, "").trim()];
}

// obstacleProblem answers what makes a blocked report's obstacle incomplete, or "" when it
// is complete. A field's value runs from its line to the next field or the footer.
export function obstacleProblem(text: string): string {
	let kind = "";
	const fields: Record<string, string> = {};
	let current = "";
	let value: string[] = [];
	const flush = () => {
		if (current) fields[current] = value.join("\n").trim();
		current = "";
		value = [];
	};
	for (const line of text.split("\n")) {
		if (line.trim().startsWith("RESULT:")) {
			flush();
			continue;
		}
		const field = fieldLine(line);
		if (field) {
			flush();
			if (field[0] === "OBSTACLE") kind = field[1].toLowerCase();
			else [current, value] = [field[0], [field[1]]];
			continue;
		}
		if (current) value.push(line);
	}
	flush();
	if (!kind && footerOf(text) === "trunk-broken") kind = "trunk";
	const kinds = Object.keys(OBSTACLE_REQUIRED).sort().join(", ");
	if (!kind) return `the report names no obstacle: a \`OBSTACLE: <kind>\` line is missing, where the kind is one of ${kinds}`;
	const required = OBSTACLE_REQUIRED[kind];
	if (!required) return `\`OBSTACLE: ${kind}\` is not an obstacle kind; the kinds are ${kinds}`;
	const missing = required.filter((name) => !(fields[name] ?? "").trim()).map((name) => `\`${name}:\``);
	if (missing.length) return `the ${kind} obstacle is missing ${missing.join(", ")}`;
	if (kind === "fence" || kind === "tooling") {
		for (const path of fields.PATHS.split(/[\s,]+/).map((p) => p.replace(/^[`*-]+|[`*-]+$/g, "")).filter(Boolean)) {
			if (path.startsWith("/") || path === ".." || path.startsWith("../")) return `the ${kind} obstacle's \`PATHS:\` must be repository-relative, and ${path} is not`;
		}
	}
	return "";
}

// obstacleKind answers the kind a report's last OBSTACLE line names, lowercased, or "".
export function obstacleKind(text: string): string {
	let kind = "";
	for (const line of text.split("\n")) {
		const field = fieldLine(line);
		if (field && field[0] === "OBSTACLE") kind = field[1].toLowerCase();
	}
	return kind;
}

// What a seat obstacle from a run that changed nothing is missing (obstacle.go).
export const SEAT_NEEDS_ATTEMPT =
	"the seat obstacle names no attempt: the worktree is unchanged from the base, so there is nothing tried for another model to start from. Make the change you can, run its tests, and name a seat obstacle only when that attempt shows what this model cannot do";

// The worker's two endings, in the words every return to a worker carries (obstacle.go's
// ObstacleContract).
export const OBSTACLE_CONTRACT =
	"A worker run has two endings. The goal is the commit, ending `RESULT: done`. The only other ending is `RESULT: blocked` with a line `OBSTACLE: <kind>` and that kind's fields: `definition` (CRITERIA, EVIDENCE) when the item's words are wrong or contradict the tree; `fence` (PATHS, EVIDENCE) when the change needs paths outside the fence; `trunk` (CHECKS, BASE) when the base is red and this change did not cause it; `tooling` (COMMAND, EVIDENCE, PATHS) when a tool or the harness is defective; `seat` (TRIED, MISSING) when an attempt you made and tested shows the task exceeds this model; `owner` (QUESTION) when only the owner can decide. A merge conflict, a patch to reconcile, a failing test inside the fence, a long suite and unfinished work are work to continue, not obstacles.";

export function decide(input: GoalInput): GoalDecision {
	if (input.paused) return { action: "settle", reason: "a pause was requested" };
	if (input.outcome !== undefined && input.outcome !== "completed") return { action: "settle", reason: `the run's outcome is ${input.outcome}` };
	if (input.stopReason !== "stop") return { action: "settle", reason: `the turn ended by ${input.stopReason ?? "no stop reason"}, not by the model` };
	const footer = footerOf(input.text);
	const committed = input.tree.head !== "" && input.tree.head !== input.base;
	const clean = input.tree.status.trim() === "";
	let obstacle = footer === "blocked" || footer === "trunk-broken" ? obstacleProblem(input.text) : undefined;
	// A seat obstacle's evidence is the attempt itself (obstacle.go's SeatNeedsAttempt).
	if (obstacle === "" && obstacleKind(input.text) === "seat" && !committed && clean) obstacle = SEAT_NEEDS_ATTEMPT;
	if (obstacle === "") return { action: "settle", reason: `the worker named a complete obstacle (RESULT: ${footer})` };
	if (committed && clean && footer) return { action: "settle", reason: "the goal is met: a commit, a clean worktree and a report" };
	const fingerprint = createHash("sha256")
		.update([input.tree.head, input.tree.status, input.tree.content, input.text.trim().replace(/\s+/g, " ")].join("\0"))
		.digest("hex");
	if (fingerprint === input.previous) {
		return { action: "settle", reason: "identical repeat: the tree and the report are unchanged since the last nudge" };
	}
	const missing: string[] = [];
	if (!committed) missing.push(`there is no commit: HEAD is still the base ${input.base.slice(0, 12)}`);
	if (!clean) {
		const paths = input.tree.status.trim().split("\n");
		const shown = paths.slice(0, 20).join("\n");
		missing.push(`these paths are uncommitted:\n${shown}${paths.length > 20 ? `\n(and ${paths.length - 20} more)` : ""}`);
	}
	if (!footer) missing.push("your last message has no RESULT footer");
	else if (footer === "done" && !committed) missing.push("it says RESULT: done, but nothing is committed");
	else if (footer === "failed") missing.push("RESULT: failed is not an ending");
	if (obstacle) missing.push(`it says RESULT: blocked, but ${obstacle}`);
	const message = [
		`The run is not finished: ${missing.join("; ")}.`,
		"",
		// Measured 2026-11-18 at two post-nudge quit points of one Sol 6.2 run: an earlier
		// "keep going" wording continued 1 of 16 replays, this one 16 of 16. The model read a
		// softer nudge as a request for one more step and another report.
		"Do not write another report: nobody reads one until the commit exists, and a report that says the work is unfinished is answered with this message again. Your next message must be a tool call that moves the work forward. Decide the change, edit the files, run the tests, and commit.",
		"",
		`End your turn only when that commit exists and its verification has run, or when an obstacle holds and you name it completely. ${OBSTACLE_CONTRACT}`,
	].join("\n");
	return { action: "continue", fingerprint, message };
}

function textOf(message: any): string {
	const content = message?.content;
	if (!Array.isArray(content)) return typeof content === "string" ? content : "";
	return content.filter((c: any) => c?.type === "text").map((c: any) => c.text ?? "").join("");
}

async function readTree(pi: any): Promise<Tree> {
	const run = async (script: string) => {
		const r = await pi.exec("sh", ["-c", script]);
		return r.code === 0 ? String(r.stdout ?? "") : "";
	};
	const head = (await run("git rev-parse HEAD")).trim();
	const status = await run("git status --porcelain=v1 --untracked-files=all");
	// The content key: tracked changes and every untracked file's blob id, so a repeat is
	// a tree that did not change at all, not one whose path list happens to match.
	const content = await run("{ git diff HEAD --binary; git ls-files -o --exclude-standard -z | xargs -0 -r git hash-object --; } | git hash-object --stdin");
	return { head, status, content: content.trim() };
}

export default function keepGoing(pi: any) {
	const base = process.env.TILLR_GOAL_BASE;
	if (!base) return;
	let last: any;
	let previous: string | undefined;
	pi.on("agent_end", (event: any) => {
		for (const message of event?.messages ?? []) if (message?.role === "assistant") last = message;
	});
	pi.on("agent_before_settle", async (event: any) => {
		const pause = process.env.TILLR_PAUSE_REQUEST;
		const decision = decide({
			base,
			outcome: event?.outcome,
			stopReason: last?.stopReason,
			text: textOf(last),
			tree: await readTree(pi),
			previous,
			paused: !!pause && existsSync(pause),
		});
		const entries = [...(event?.entries ?? [])];
		if (decision.action === "settle") {
			entries.push({ type: "custom", customType: "tillr-goal", data: { action: "settle", reason: decision.reason } });
			return { entries };
		}
		previous = decision.fingerprint;
		entries.push({ type: "custom_message", customType: "tillr-goal", content: decision.message, display: true });
		return { entries, continue: true };
	});
}
