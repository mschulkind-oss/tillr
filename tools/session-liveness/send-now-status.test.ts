/**
 * Interactive send-now must say which state it is in.
 *
 * `handleSendNow` refuses while `immediatePromptInFlight` is set, and the refusal prints one fixed
 * sentence: "Sending queued messages now: waiting for the current turn to cancel". That sentence is
 * wrong in one of the two states it covers — when the flag is stuck and no turn is being cancelled
 * at all (the compaction-only hang), the user is told to wait for a cancellation that does not
 * exist. The status has to distinguish "the flag is stuck, nothing is in flight" from "an abort is
 * in flight".
 *
 * Both states are driven through the production handler, not by hand-set fields:
 *
 * - stuck: a compaction-only session, a queued message, and an empty editor take the batch path,
 *   whose `sendAllQueuedNow()` never settles, so the flag stays true with nothing in flight;
 * - aborting: a streaming run that ignores its abort signal, so the batch send's abort stays in
 *   flight while the flag is true.
 *
 * RED against the current fork: both states print the same sentence.
 */

import { afterEach, describe, expect, it } from "vitest";
import {
	createHarness,
	handleSendNow,
	settleWithin,
	startStuckCompaction,
	waitFor,
	type QueuedMessageHarness,
} from "./harness.ts";

let harness: QueuedMessageHarness | undefined;

afterEach(() => {
	harness?.dispose();
	harness = undefined;
});

describe("interactive send-now status", () => {
	it("interactive send-now reports which state it is in", async () => {
		// State A: the flag is stuck and nothing is in flight.
		harness = await createHarness({
			ignoreAbortSignal: true,
			settingsOverrides: { compaction: { keepRecentTokens: 1 } },
		});
		let h = harness;
		await startStuckCompaction(h);
		await h.session.steer("queued during compaction");
		const stuckSend = handleSendNow(h.controller.controller);
		stuckSend.catch(() => {});
		expect(
			await waitFor(() => h.controller.controller.immediatePromptInFlight === true, 1_000),
			"the first send-now never set immediatePromptInFlight",
		).toBe(true);
		expect(h.session.isStreaming, "the fixture expected no run to be active").toBe(false);

		await handleSendNow(h.controller.controller);
		const stuckStatus = h.controller.statuses.at(-1) ?? "";

		harness.dispose();
		harness = undefined;

		// State B: the flag is stuck while an abort is in flight.
		harness = await createHarness({ ignoreAbortSignal: true });
		h = harness;
		const run = h.session.prompt("first");
		run.catch(() => {});
		await h.nextTurn();
		await h.session.prompt("queued", { streamingBehavior: "steer" });
		const abortingSend = handleSendNow(h.controller.controller);
		abortingSend.catch(() => {});
		expect(
			await waitFor(() => h.controller.controller.immediatePromptInFlight === true, 1_000),
			"the first send-now never set immediatePromptInFlight",
		).toBe(true);
		// The abort is in flight: the run ignores its signal and the send is still waiting on it.
		expect(h.session.isStreaming, "the run was not streaming, so no abort was in flight").toBe(true);
		expect(
			(await settleWithin(abortingSend, 100)).settled,
			"the aborting send settled; the abort-in-flight state was not reached",
		).toBe(false);

		await handleSendNow(h.controller.controller);
		const abortStatus = h.controller.statuses.at(-1) ?? "";

		expect(stuckStatus, "the stuck-flag state produced no status").not.toBe("");
		expect(abortStatus, "the abort-in-flight state produced no status").not.toBe("");
		expect(
			stuckStatus,
			`both states print the same status ("${stuckStatus}"): a stuck immediatePromptInFlight with nothing in flight and an abort in flight are indistinguishable`,
		).not.toBe(abortStatus);
		expect(
			/abort|cancel/i.test(stuckStatus),
			`the stuck-flag status ("${stuckStatus}") says a turn is cancelling while nothing is in flight`,
		).toBe(false);
		expect(
			/abort|cancel/i.test(abortStatus),
			`the abort-in-flight status ("${abortStatus}") does not say that an abort is in flight`,
		).toBe(true);
	});
});
