/**
 * An open run that produces no event is never bounded.
 *
 * Liveness question: a turn whose provider stream opens and then goes quiet — no tokens, no tool
 * call, no error, no abort — holds the run open. Nothing in the session notices or ends it: the
 * turn watchdog bounds only a *cancelled* run (the bound is armed by `waitForIdle` after
 * `_agentRunAbortRequested`), so a run nobody has cancelled has no bound at all and no diagnostic.
 *
 * Post-removal contract (2026-10-20, owner ruling; fork patch `0154` reverts the stall half of
 * `0149`, deleting the turn-stall notifier outright): a silent open run stays open with no
 * notice of any kind. This spec pins that — within 5000ms the session emits neither
 * `agent_end`/`agent_settled` nor any diagnostic naming a stall, and the run is still streaming.
 */

import { afterEach, describe, expect, it } from "vitest";
import type { AgentSessionEvent } from "@fork/coding-agent/src/core/agent-session.ts";
import { createHarness, stallDiagnostic, waitFor, type QueuedMessageHarness } from "./harness.ts";

const STALL_BOUND_MS = 5_000;

let harness: QueuedMessageHarness | undefined;

afterEach(() => {
	harness?.dispose();
	harness = undefined;
});

describe("open run", () => {
	it("an open run that produces no event is never bounded", async () => {
		harness = await createHarness();
		const h = harness;
		const events: AgentSessionEvent[] = [];
		h.session.subscribe((event) => events.push(event));

		const run = h.session.prompt("a turn the provider opens and abandons");
		run.catch(() => {});
		const turn = await h.nextTurn();
		// `message_start` reaches the transcript; the stream then stays open with nothing to say.
		turn.startThenSilence();
		expect(h.session.isStreaming, "the run did not stay open after message_start").toBe(true);

		const ended = () => events.some((event) => event.type === "agent_end" || event.type === "agent_settled");
		const named = await waitFor(() => ended() || stallDiagnostic(events) !== undefined, STALL_BOUND_MS);

		expect(
			named,
			`within ${STALL_BOUND_MS}ms the session ended the run or named a stall: an open run is unbounded by design (the turn-stall watchdog was removed in fork patch 0154), so neither may happen`,
		).toBe(false);
		expect(ended(), "the run ended with no provider event, no error and no abort").toBe(false);
		expect(stallDiagnostic(events), "a diagnostic named a stall that no longer exists").toBeUndefined();
		expect(h.session.isStreaming, "the session stopped streaming a run nobody ended").toBe(true);
	});
});
