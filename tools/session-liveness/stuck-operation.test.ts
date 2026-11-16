/**
 * A session that is open, idle-looking, and unrecoverable.
 *
 * These are liveness specs: from the state each test reaches, does the session return to idle
 * within a bound, and does a later send-now actually start work? Each fails on its own assertion
 * (a bounded race), naming the leak it found — never by timing out the file.
 *
 * The states:
 *
 * 1. A compaction is running and no agent run is. The abort requests no run abort, so the wait has
 *    no turn watchdog to release it; the session's own compaction grace is what bounds it, and the
 *    abandonment that follows has to leave the session genuinely idle rather than merely releasing
 *    the wait: `isCompacting` false, and a prompt the manual controller would refuse accepted again.
 * 2. A cancelled run retires before the watchdog fires, so `_releaseTimedOutTurn()` returns early;
 *    the idle wait it was created for must be resolved, not left for the next plain wait to inherit.
 * 3. The stuck operation in (1) is stored in `_immediatePromptPromise`, so a later send-now returns
 *    it instead of starting a new operation.
 * 4. A branch summary alone is running, with no agent run and no compaction: the same bound has to
 *    cover it, and the same abandonment has to clear it.
 */

import { afterEach, describe, expect, it } from "vitest";
import {
	bounded,
	createHarness,
	sessionInternals,
	setSessionAutoCompacting,
	settleWithin,
	startStuckCompaction,
	type QueuedMessageHarness,
} from "./harness.ts";

let harness: QueuedMessageHarness | undefined;

afterEach(() => {
	harness?.dispose();
	harness = undefined;
});

/**
 * The state test 1 is named for: compaction running, no agent run, and a queued user message so the
 * batch send has something to promote.
 */
async function reachCompactionOnlyState(): Promise<QueuedMessageHarness> {
	harness = await createHarness({
		ignoreAbortSignal: true,
		settingsOverrides: { compaction: { keepRecentTokens: 1 } },
	});
	const h = harness;
	await startStuckCompaction(h);
	expect(h.session.isCompacting, "the compaction did not reach the running state").toBe(true);
	expect(h.session.isStreaming, "the fixture expected a compaction with no agent run").toBe(false);
	await h.session.steer("queued during compaction");
	return h;
}

describe("stuck operations", () => {
	it("compaction-only abort never settles", async () => {
		const h = await reachCompactionOnlyState();

		const send = h.session.sendAllQueuedNow();
		// The operation never settles; the test observes it through a bound instead of awaiting it.
		send.catch(() => {});
		const outcome = await settleWithin(send, 3_000);

		if (!outcome.settled) {
			// The cause, asserted only in the red branch so a fix is judged on the contract: an idle
			// wait exists and no abort was requested, so waitForIdle took its unbounded branch.
			const internals = sessionInternals(h.session);
			expect(
				internals.idleWaitPending,
				"no idle wait was created: the send is stuck somewhere other than waitForIdle",
			).toBe(true);
			expect(
				internals.agentRunAbortRequested,
				"the compaction-only abort did request a run abort, so the watchdog would have armed",
			).toBe(false);
		}

		expect(
			outcome.settled,
			"sendAllQueuedNow did not settle within 3000ms: the compaction-only abort armed no bound, so the wait for the compaction that ignored its abort never ended",
		).toBe(true);
	});

	it("an abandoned compaction stops holding the session", async () => {
		const h = await reachCompactionOnlyState();

		const send = h.session.sendAllQueuedNow();
		send.catch(() => {});
		expect(
			(await settleWithin(send, 3_000)).settled,
			"the send never gave up on the compaction that ignored its abort",
		).toBe(true);

		// Releasing the wait is not enough: the compaction's registration is what `isCompacting` reads,
		// and the summarization that ignored its abort never reaches the `finally` that clears it.
		expect(
			h.session.isCompacting,
			"the session still claims a compaction is running after abandoning the one that ignored its abort",
		).toBe(false);
		expect(h.session.isIdle, "the abandoned compaction still holds the session open").toBe(true);

		// The manual controller is the one `prompt()` refuses on, so accepting a prompt is the state's
		// real proof: a released wait over a session that still refuses every prompt is not liveness.
		let refusal: unknown;
		let after: Promise<void> | undefined;
		try {
			after = h.session.prompt("after the abandoned compaction");
		} catch (error) {
			refusal = error;
		}
		expect(
			refusal === undefined,
			`the abandoned compaction still refuses a prompt: ${refusal instanceof Error ? refusal.message : String(refusal)}`,
		).toBe(true);
		if (after) {
			const turn = await h.nextTurn(2_000);
			turn.respond();
			await bounded(after, 3_000, "the prompt after the abandoned compaction");
		}
	});

	it("a branch-summary-only abort is bounded and leaves nothing compacting", async () => {
		harness = await createHarness({
			ignoreAbortSignal: true,
			settingsOverrides: { retry: { enabled: false, maxAgentDelayMs: 100 } },
		});
		const h = harness;
		const model = h.session.model!;
		const first = h.session.sessionManager.appendMessage({
			role: "user",
			content: [{ type: "text", text: "the exchange to summarize" }],
			timestamp: Date.now() - 2_000,
		});
		h.session.sessionManager.appendMessage({
			role: "assistant",
			content: [{ type: "text", text: "assistant response to summarize" }],
			api: model.api,
			provider: model.provider,
			model: model.id,
			usage: {
				input: 100,
				output: 0,
				cacheRead: 0,
				cacheWrite: 0,
				totalTokens: 100,
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
			},
			stopReason: "stop",
			timestamp: Date.now() - 1_000,
		});
		h.session.sessionManager.appendMessage({
			role: "user",
			content: [{ type: "text", text: "the branch that gets abandoned" }],
			timestamp: Date.now() - 500,
		});

		// Navigating away with `summarize` runs a branch summary and nothing else: no agent run, and no
		// compaction. Its summarization request ignores its abort, so only the session's bound ends it.
		const summary = h.session.navigateTree(first, { summarize: true });
		summary.catch(() => {});
		await h.nextTurn();
		expect(h.session.isCompacting, "the branch summary did not reach the running state").toBe(true);
		expect(h.session.isStreaming, "the fixture expected a branch summary with no agent run").toBe(false);

		const aborting = h.session.abort();
		expect(
			(await settleWithin(aborting, 3_000)).settled,
			"the branch-summary-only abort did not settle within 3000ms: no bound was armed for it",
		).toBe(true);
		expect(
			h.session.isCompacting,
			"the session still claims a branch summary is running after abandoning the one that ignored its abort",
		).toBe(false);
		expect(h.session.isIdle, "the abandoned branch summary still holds the session open").toBe(true);
	});

	it("timed-out settle leaves the idle wait unresolved", async () => {
		harness = await createHarness({ settingsOverrides: { retry: { enabled: false, maxAgentDelayMs: 100 } } });
		const h = harness;
		// Auto compaction: `isCompacting` true while `prompt()` is still accepted, which is the only
		// shape in which a run can start again while the compaction keeps the session non-idle.
		setSessionAutoCompacting(h.session, true);

		const firstRun = h.session.prompt("first");
		await h.nextTurn();
		const aborting = h.session.abort();
		// The run settles from its own abort before the 100ms bound; the abort's waitForIdle then
		// finds the run already retired and `_releaseTimedOutTurn` returns early.
		await bounded(firstRun, 3_000, "first run");
		await bounded(aborting, 3_000, "abort");
		expect(h.session.isStreaming, "the run was still active when the bound fired").toBe(false);

		// The bound fired with the run already retired, so what held the wait was the compaction that
		// ignored its abort. `_releaseTimedOutTurn` takes its early return there, and the abandonment
		// it performs has to leave the session idle: no registration, and no wait left behind.
		const afterEarlyReturn = sessionInternals(h.session);
		expect(afterEarlyReturn.isAgentRunActive, "the run had not retired when the bound fired").toBe(false);
		expect(
			h.session.isCompacting,
			"the session still claims a compaction is running after the bound released the wait",
		).toBe(false);
		expect(h.session.isIdle, "the abandoned compaction still holds the session open").toBe(true);
		expect(
			afterEarlyReturn.idleWaitPending,
			"the timed-out settle left its idle wait unresolved for the next caller to inherit",
		).toBe(false);

		// The contract: a later plain wait settles. A new run clears `_agentRunAbortRequested`, so this
		// wait takes the plain, unbounded path with no watchdog to rescue it.
		const secondRun = h.session.prompt("second");
		const secondTurn = await h.nextTurn(3_000);
		const idle = h.session.waitForIdle();
		const beforeRespond = sessionInternals(h.session);
		expect(
			beforeRespond.agentRunAbortRequested,
			"the second wait armed a watchdog; it was not the plain path",
		).toBe(false);
		expect(
			beforeRespond.idleWaitPending,
			"the plain wait created no idle wait, so the wait under test was a no-op",
		).toBe(true);
		secondTurn.respond();
		await bounded(secondRun, 3_000, "second run");

		const outcome = await settleWithin(idle, 2_000);
		expect(
			outcome.settled,
			"waitForIdle did not resolve within 2000ms after the second run settled",
		).toBe(true);
	});

	it("a later send-now is a no-op after a stuck operation", async () => {
		const h = await reachCompactionOnlyState();

		const first = h.session.sendAllQueuedNow();
		first.catch(() => {});
		const stuck = await settleWithin(first, 500);
		expect(stuck.settled, "the first send settled, so the stuck-operation state was not reached").toBe(false);

		// A message the user queues after the first send, then a second Ctrl+Enter.
		await h.session.steer("queued after the stuck send");
		const second = h.session.sendAllQueuedNow();
		second.catch(() => {});
		const fresh = await h.nextTurn(2_000).then(
			(turn) => turn,
			() => undefined,
		);
		if (fresh === undefined) {
			expect(
				sessionInternals(h.session).immediatePromptStored,
				"the stuck operation was not stored in _immediatePromptPromise; the no-op has a different cause",
			).toBe(true);
		}

		expect(
			fresh,
			`the second sendAllQueuedNow started no new operation within 2000ms${
				second === first ? ": it returned the stored _immediatePromptPromise, so the stuck operation is re-entered instead of a fresh one" : ""
			}`,
		).toBeDefined();
	});
});
