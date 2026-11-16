/**
 * Fixtures that drive pi's session-liveness states without a TUI.
 *
 * This is [`tools/queued-message-states/harness.ts`](../queued-message-states/harness.ts) copied,
 * because that harness's specs must not be edited to reach the states measured here. Two seams are
 * added: `Turn.startThenSilence()` (a run that emits `message_start` and nothing else) and
 * `setSessionAutoCompacting()` (compaction running through the auto controller, which unlike the
 * manual one still accepts a prompt). Everything below the seams is the copy.
 *
 * Two seams are used, both from the fork checkout the handed root names (`PI_FORK`, which is
 * what `run` and the vitest configuration read; by default `<repo>/.forks/pi`):
 *
 * - `AgentSession` is constructed directly, exactly as the fork's own
 *   `packages/coding-agent/test/agent-session-*.test.ts` do. A controllable
 *   `streamFn` decides when a turn streams, stops, errors or aborts, so every
 *   queue state is reachable without a model.
 * - `InteractiveMode` methods are called on an object created from the real
 *   prototype (`Object.create(InteractiveMode.prototype)`), the seam the fork's
 *   `agent-session-immediate-prompt.test.ts` established. Only the fields the
 *   method reads are supplied; the method body under test is the production one.
 *   `setupKeyHandlers` and `setupEditorSubmitHandler` install the production
 *   key and submit handlers on it.
 *
 * The compaction state has two forms: `setSessionCompacting` sets the compaction
 * abort controller that defines `session.isCompacting`, and `session.compact()`
 * runs a real summarization request through the same `streamFn`.
 *
 * A turn may also throw from the stream's iterator (`Turn.throwMidTurn`): the
 * `StreamFn` contract forbids it, which is why it is measured separately from an
 * `error` event.
 */

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	type AssistantMessage,
	type AssistantMessageEventStream,
	createAssistantMessageEventStream,
	fauxAssistantMessage,
	type Model,
} from "@earendil-works/pi-ai";
import { Agent, type AgentMessage } from "@earendil-works/pi-agent-core";
import {
	AgentSession,
	type AgentSessionEvent,
} from "@fork/coding-agent/src/core/agent-session.ts";
import { AuthStorage } from "@fork/coding-agent/src/core/auth-storage.ts";
import { SessionManager } from "@fork/coding-agent/src/core/session-manager.ts";
import { SettingsManager } from "@fork/coding-agent/src/core/settings-manager.ts";
import type { Settings } from "@fork/coding-agent/src/core/settings-schema.ts";
import { InteractiveMode } from "@fork/coding-agent/src/modes/interactive/interactive-mode.ts";
import {
	createModelRegistry,
	getModelRuntime,
} from "@fork/coding-agent/test/model-runtime-test-utils.ts";
import { createTestResourceLoader } from "@fork/coding-agent/test/utilities.ts";

const TEST_MODEL: Model<any> = {
	id: "queued-message-test-model",
	name: "Queued message test model",
	api: "anthropic-messages",
	provider: "anthropic",
	baseUrl: "https://example.invalid",
	reasoning: false,
	input: ["text"],
	cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
	contextWindow: 100_000,
	maxTokens: 1024,
};

export type Turn = {
	/** User prompt texts visible to the model in this request, in transcript order. */
	users: string[];
	/** The request has been answered; further answers are ignored. */
	settled: boolean;
	/** A test has already taken this request from `nextTurn()`. */
	claimed: boolean;
	/** Answer with a normal stop. */
	respond(text?: string): void;
	/** Answer with a provider error (`stopReason: "error"`). */
	fail(errorMessage: string): void;
	/** Answer with an abort (`stopReason: "aborted"`). */
	abort(): void;
	/**
	 * Throw from the stream's iterator after the partial message: a provider that dies mid-turn
	 * instead of reporting an error event. The `StreamFn` contract forbids this; the point is to
	 * measure what a provider that breaks it leaves behind.
	 */
	throwMidTurn(errorMessage: string): void;
	/** Keep the stream open even when the run is aborted: the turn never settles. */
	holdOpen(): void;
	/**
	 * Emit the opening `message_start` and then nothing: a provider that opened a turn and went
	 * quiet. Nothing is aborted, so no cancellation bound is armed; the run stays open.
	 */
	startThenSilence(): void;
};

export type QueuedMessageHarness = {
	session: AgentSession;
	agent: Agent;
	requests: Turn[];
	/** Resolves with the next model request; rejects after `timeoutMs`. */
	nextTurn(timeoutMs?: number): Promise<Turn>;
	/** The request that is streaming and not yet answered. */
	pendingTurn(): Turn | undefined;
	/** The Ctrl+Enter / Escape controller double backed by the real prototype. */
	controller: InteractiveModeDouble;
	dispose(): void;
};

type Waiter = { resolve: (turn: Turn) => void; reject: (error: Error) => void };

function textOf(message: AgentMessage): string {
	if (message.role !== "user") return "";
	if (typeof message.content === "string") return message.content;
	return message.content
		.filter((part): part is { type: "text"; text: string } => part.type === "text")
		.map((part) => part.text)
		.join("");
}

function userTexts(messages: readonly AgentMessage[]): string[] {
	return messages.filter((message) => message.role === "user").map(textOf);
}

function settledAssistant(text: string, stopReason: AssistantMessage["stopReason"], errorMessage?: string) {
	return fauxAssistantMessage(text, {
		stopReason,
		...(errorMessage === undefined ? {} : { errorMessage }),
	});
}

export type HarnessOptions = {
	/** Settings merged over the defaults, e.g. `{ retry: { enabled: false } }`. */
	settingsOverrides?: Partial<Settings>;
	/**
	 * The stream ignores its abort signal and never answers: a turn that never settles. A real
	 * provider stream is expected to settle when aborted; this models the one that does not.
	 */
	ignoreAbortSignal?: boolean;
};

export async function createHarness(options: HarnessOptions = {}): Promise<QueuedMessageHarness> {
	const ignoreAbortSignal = options.ignoreAbortSignal === true;
	const tempDir = mkdtempSync(join(tmpdir(), "queued-message-states-"));
	const requests: Turn[] = [];
	const waiters: Waiter[] = [];

	const claim = (turn: Turn): Turn => {
		turn.claimed = true;
		return turn;
	};

	const pushTurn = (turn: Turn): void => {
		requests.push(turn);
		const waiter = waiters.shift();
		if (waiter) waiter.resolve(claim(turn));
	};

	const nextTurn = (timeoutMs = 2_000): Promise<Turn> => {
		const unclaimed = requests.find((turn) => !turn.claimed);
		if (unclaimed) return Promise.resolve(claim(unclaimed));
		return new Promise<Turn>((resolve, reject) => {
			const timer = setTimeout(() => {
				const index = waiters.findIndex((waiter) => waiter.resolve === resolve);
				if (index !== -1) waiters.splice(index, 1);
				reject(new Error(`No model request started within ${timeoutMs}ms`));
			}, timeoutMs);
			waiters.push({
				resolve: (turn) => {
					clearTimeout(timer);
					resolve(turn);
				},
				reject: (error) => {
					clearTimeout(timer);
					reject(error);
				},
			});
		});
	};

	const streamFn = (_model: Model<any>, context: { messages: AgentMessage[] }, options?: { signal?: AbortSignal }) => {
		const stream = createAssistantMessageEventStream();
		let heldOpen = false;
		let throwError: Error | undefined;
		const turn: Turn = {
			users: userTexts(context.messages),
			settled: false,
			claimed: false,
			respond: (text = "done") => {
				if (turn.settled) return;
				turn.settled = true;
				stream.push({ type: "done", reason: "stop", message: settledAssistant(text, "stop") });
			},
			fail: (errorMessage) => {
				if (turn.settled) return;
				turn.settled = true;
				stream.push({ type: "error", reason: "error", error: settledAssistant("", "error", errorMessage) });
			},
			abort: () => {
				if (turn.settled) return;
				turn.settled = true;
				stream.push({
					type: "error",
					reason: "aborted",
					error: settledAssistant("", "aborted", "Request was aborted."),
				});
			},
			throwMidTurn: (errorMessage) => {
				if (turn.settled) return;
				turn.settled = true;
				// The partial message reaches the transcript first; the throw lands on the iterator's
				// next read, so the run fails after `message_start`, the way a dropped provider does.
				stream.push({ type: "start", partial: settledAssistant("", "stop") });
				throwError = new Error(errorMessage);
			},
			holdOpen: () => {
				heldOpen = true;
			},
			startThenSilence: () => {
				stream.push({ type: "start", partial: settledAssistant("", "stop") });
			},
		};
		pushTurn(turn);
		// A real provider stream settles when its signal aborts. Answering here keeps
		// `session.abort()` from waiting on a turn no test is going to answer; a turn held
		// open — by `holdOpen()` or by the harness's `ignoreAbortSignal` — ignores the
		// abort, which is how a run that never settles is reached.
		const settleOnAbort = () => {
			if (!heldOpen && !ignoreAbortSignal) turn.abort();
		};
		if (options?.signal?.aborted) settleOnAbort();
		else options?.signal?.addEventListener("abort", settleOnAbort, { once: true });
		// The iterator is the only seam that can throw: the stream itself has no error channel, so a
		// throwing turn is a wrapper around it rather than a pushed event.
		return {
			result: () => stream.result(),
			async *[Symbol.asyncIterator]() {
				for await (const event of stream) {
					yield event;
					if (throwError) {
						const error = throwError;
						throwError = undefined;
						throw error;
					}
				}
			},
		} as unknown as AssistantMessageEventStream;
	};

	const agent = new Agent({
		getApiKey: () => "test-key",
		initialState: { model: TEST_MODEL, systemPrompt: "Test", tools: [] },
		streamFn,
	});

	const authStorage = AuthStorage.create(join(tempDir, "auth.json"));
	await authStorage.modify("anthropic", async () => ({ type: "api_key", key: "test-key" }));
	const modelRegistry = await createModelRegistry(authStorage, tempDir);
	const settingsManager = SettingsManager.create(tempDir, tempDir);
	if (options.settingsOverrides) {
		settingsManager.applyOverrides(options.settingsOverrides);
	}
	const session = new AgentSession({
		agent,
		sessionManager: SessionManager.inMemory(tempDir),
		settingsManager,
		cwd: tempDir,
		modelRuntime: getModelRuntime(modelRegistry),
		resourceLoader: createTestResourceLoader(),
	});
	session.subscribe(() => {});

	const controller = createInteractiveController(session);

	return {
		session,
		agent,
		requests,
		nextTurn,
		pendingTurn: () => requests.findLast((turn) => !turn.settled),
		controller,
		dispose: () => {
			for (const waiter of waiters.splice(0)) waiter.reject(new Error("Harness disposed"));
			session.dispose();
			rmSync(tempDir, { recursive: true, force: true });
		},
	};
}

/**
 * The reason the turn watchdog recorded when it stopped waiting for a cancelled turn, or
 * `undefined` when no turn was abandoned. It is the terminal error a failed run appends, so
 * finding it here is the same evidence the transcript and the UI carry.
 */
export function turnWatchdogReason(events: readonly AgentSessionEvent[]): string | undefined {
	for (const event of events) {
		if (event.type !== "message_end" || event.message.role !== "assistant") continue;
		if (event.message.stopReason === "error" && /did not settle/.test(event.message.errorMessage ?? "")) {
			return event.message.errorMessage;
		}
	}
	return undefined;
}

/** Settles `promise` or rejects after `timeoutMs`, so a hang is a failure, not a stalled suite. */
export function bounded<T>(promise: Promise<T>, timeoutMs = 2_000, what = "operation"): Promise<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	return Promise.race([
		promise,
		new Promise<T>((_resolve, reject) => {
			timer = setTimeout(() => reject(new Error(`${what} did not settle within ${timeoutMs}ms`)), timeoutMs);
		}),
	]).finally(() => {
		if (timer) clearTimeout(timer);
	});
}

type EditorDouble = {
	getText(): string;
	getExpandedText?(): string;
	getExpandedTextWithPasteRanges?(): { text: string; pasteRanges: { start: number; end: number }[] };
	setText(text: string): void;
	addToHistory(text: string): void;
	onAction?(name: string, handler: () => void): void;
	onEscape?: () => void;
	onSubmit?(text: string, options?: unknown): Promise<void> | void;
};

export type InteractiveModeDouble = {
	controller: Record<string, unknown>;
	editor: EditorDouble;
	actions: Map<string, () => void>;
	/** Every status line the mode showed, in order. */
	statuses: string[];
	/** Every error line the mode showed, in order. */
	errors: string[];
};

/**
 * The controller double the fork's immediate-prompt suite uses: an object whose
 * prototype is the real `InteractiveMode.prototype`, so the methods invoked on it
 * are the production ones. Only fields are supplied; the key and submit handlers
 * are installed by the production setup methods.
 */
export function createInteractiveController(session: AgentSession, initialText = ""): InteractiveModeDouble {
	let editorText = initialText;
	const actions = new Map<string, () => void>();
	const statuses: string[] = [];
	const errors: string[] = [];
	const editor: EditorDouble = {
		getText: () => editorText,
		setText: (text: string) => {
			editorText = text;
		},
		addToHistory: () => {},
		onAction: (name: string, handler: () => void) => actions.set(name, handler),
	};
	const controller = Object.create(InteractiveMode.prototype) as Record<string, unknown>;
	Object.defineProperties(controller, {
		session: { value: session, configurable: true },
		editor: { value: editor, configurable: true },
		defaultEditor: { value: editor, configurable: true },
		ui: { value: { requestRender: () => {} }, configurable: true },
		immediatePromptInFlight: { value: false, writable: true, configurable: true },
		compactionQueuedMessages: { value: [], writable: true, configurable: true },
		compactionQueueRetryArmed: { value: false, writable: true, configurable: true },
		updatePendingMessagesDisplay: { value: () => {}, configurable: true },
		showError: {
			value: (message: string) => {
				errors.push(message);
			},
			configurable: true,
		},
		showStatus: {
			value: (message: string) => {
				statuses.push(message);
			},
			configurable: true,
		},
		isBashMode: { value: false, writable: true, configurable: true },
		lastEscapeTime: { value: 0, writable: true, configurable: true },
		settingsManager: { value: { getDoubleEscapeAction: () => "none" }, configurable: true },
	});
	const setupKeyHandlers = Reflect.get(InteractiveMode.prototype, "setupKeyHandlers") as (this: unknown) => void;
	setupKeyHandlers.call(controller);
	const setupEditorSubmitHandler = Reflect.get(InteractiveMode.prototype, "setupEditorSubmitHandler") as (
		this: unknown,
	) => void;
	setupEditorSubmitHandler.call(controller);
	return {
		controller,
		editor,
		actions,
		statuses,
		errors,
	};
}

/** The production Ctrl+Enter handler, called on the double. */
export function handleSendNow(controller: Record<string, unknown>): Promise<void> {
	const method = Reflect.get(InteractiveMode.prototype, "handleSendNow") as (this: unknown) => Promise<void>;
	return method.call(controller);
}

/** The production Alt+Enter handler, called on the double. */
export function handleFollowUp(controller: Record<string, unknown>): Promise<void> {
	const method = Reflect.get(InteractiveMode.prototype, "handleFollowUp") as (this: unknown) => Promise<void>;
	return method.call(controller);
}

/** The production compaction-queue drain, called where the `compaction_end` listener calls it. */
export function flushCompactionQueue(
	controller: Record<string, unknown>,
	options?: { willRetry?: boolean; retry?: boolean },
): Promise<void> {
	const method = Reflect.get(InteractiveMode.prototype, "flushCompactionQueue") as (
		this: unknown,
		options?: { willRetry?: boolean; retry?: boolean },
	) => Promise<void>;
	return method.call(controller, options);
}

/**
 * Put the session in the state `InteractiveMode` reads as "compaction running".
 * `isCompacting` is defined by the compaction abort controller, so setting that
 * controller is what starts the state; no summarization request runs.
 */
export function setSessionCompacting(session: AgentSession, compacting: boolean): void {
	(session as unknown as { _compactionAbortController: AbortController | undefined })._compactionAbortController =
		compacting ? new AbortController() : undefined;
}

/** The production Escape handler, installed by `setupKeyHandlers` on the double. */
export function pressEscape(controller: InteractiveModeDouble): void {
	controller.editor.onEscape?.();
}

/**
 * Put the session in the state an *auto* compaction sets.
 *
 * `isCompacting` is defined by any of three abort controllers. The manual one is what
 * `setSessionCompacting` sets, and `prompt()` refuses while it is defined ("Cannot submit a prompt
 * while compaction is in progress"). The auto controller carries the same running state and leaves
 * `prompt()` accepted, which is the state a liveness check needs: a run can start while compaction
 * is still running.
 */
export function setSessionAutoCompacting(session: AgentSession, compacting: boolean): void {
	(session as unknown as { _autoCompactionAbortController: AbortController | undefined })._autoCompactionAbortController =
		compacting ? new AbortController() : undefined;
}

/**
 * Start a real manual compaction whose summarization request never settles.
 *
 * The request goes through the harness's `streamFn` with the compaction controller's signal, so a
 * harness created with `ignoreAbortSignal` leaves `isCompacting` true after `abortCompaction()`:
 * nothing is running an agent run, and nothing will ever clear the compaction state.
 */
export async function startStuckCompaction(
	h: QueuedMessageHarness,
): Promise<{ compacting: Promise<unknown>; summary: Turn }> {
	const session = h.session;
	const model = session.model;
	if (!model) throw new Error("The harness session has no model");
	session.sessionManager.appendMessage({
		role: "user",
		content: [{ type: "text", text: "message to compact" }],
		timestamp: Date.now() - 1_000,
	});
	session.sessionManager.appendMessage({
		role: "assistant",
		content: [{ type: "text", text: "assistant response to compact" }],
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
		timestamp: Date.now() - 500,
	});
	const compacting = session.compact();
	// The compaction never ends, so its promise never settles; it is awaited only through a bound.
	compacting.catch(() => {});
	const summary = await h.nextTurn();
	return { compacting, summary };
}

export type SettleOutcome<T> = { settled: true; value: T } | { settled: false };

/**
 * The private fields a liveness leak is named for.
 *
 * A spec that says "the wait had no watchdog" or "the stored operation was returned" has to be able
 * to fail on that claim, not only on the timeout that follows it, or a red run proves nothing about
 * the cause. Reading them here keeps the names in one place.
 */
export type SessionInternals = {
	agentRunAbortRequested: boolean;
	isAgentRunActive: boolean;
	idleWaitPending: boolean;
	immediatePromptStored: boolean;
};

export function sessionInternals(session: AgentSession): SessionInternals {
	const raw = session as unknown as {
		_agentRunAbortRequested: boolean;
		_isAgentRunActive: boolean;
		_idleWaitPromise: unknown;
		_immediatePromptPromise: unknown;
	};
	return {
		agentRunAbortRequested: raw._agentRunAbortRequested,
		isAgentRunActive: raw._isAgentRunActive,
		idleWaitPending: raw._idleWaitPromise !== undefined,
		immediatePromptStored: raw._immediatePromptPromise !== undefined,
	};
}

/**
 * Whether `promise` settles within `timeoutMs`, without rejecting when it does not.
 *
 * A hang is the expected result in these specs, so the caller asserts on the outcome and names the
 * leak in its own failure message; `bounded()` above rejects with a generic one.
 */
export async function settleWithin<T>(promise: Promise<T>, timeoutMs: number): Promise<SettleOutcome<T>> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	const timeout = new Promise<SettleOutcome<T>>((resolve) => {
		timer = setTimeout(() => resolve({ settled: false }), timeoutMs);
	});
	try {
		return await Promise.race([promise.then((value) => ({ settled: true as const, value })), timeout]);
	} finally {
		if (timer) clearTimeout(timer);
	}
}

/** Poll `predicate` until it is true or `timeoutMs` elapses; returns the final verdict. */
export async function waitFor(
	predicate: () => boolean,
	timeoutMs: number,
	intervalMs = 25,
): Promise<boolean> {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		if (predicate()) return true;
		await new Promise((resolve) => setTimeout(resolve, intervalMs));
	}
	return predicate();
}

/**
 * The first session event that names a stall, or `undefined`.
 *
 * A run that opens and then emits nothing has no event of its own; a diagnostic would have to name
 * what is wrong, so any emitted text that says "stall" (or that the stream emitted nothing) counts.
 */
export function stallDiagnostic(events: readonly AgentSessionEvent[]): string | undefined {
	for (const event of events) {
		const text = JSON.stringify(event);
		if (/stall|emitted no event|did not emit/i.test(text)) return text;
	}
	return undefined;
}
