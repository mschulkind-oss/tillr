# Tillr: Driving Motivation

## The Core Problem

When humans work with AI agents on software, there's a fundamental gap: **the human can't see what's happening, can't tell what's right, and can't steer effectively.**

Agents produce code, make decisions, and change things — but from the human's perspective, the result is either a wall of diffs to review or a "trust me, it's done." Neither is acceptable for real work.

## What We're Building

Tillr is an interface that lets humans **find and hold the line** between what they need to verify and what they can trust agents to handle. It's not about removing humans from the loop — it's about making the loop legible, so humans can:

1. **Understand the conversation** — see what agents are doing, why, and what decisions they're making, without reading every line of code
2. **Drive the direction** — steer agents toward the right work in the right order, change priorities on the fly, redirect mid-stream
3. **Verify what matters** — QA the things a human can actually judge (does it look right? does it feel right? does it solve the problem?) and let agents verify the rest (does it compile? do tests pass? are APIs correct?)

## The Verification Problem

Today's QA checklists are written for developers: "verify the database index exists," "check the CLI flag accepts comma-separated values." Humans reviewing agent output can't — and shouldn't — do this. They should be asking:

- Does this feature solve the problem I described?
- Does the UI make sense?
- Is the behavior what I expected?
- What don't I understand that I should?

The technical verification (tests pass, types check, no regressions) is the agent's job. The human's job is **judgment** — does this thing belong in the product?

## The Orchestration Problem

Agents today get ad-hoc prompts: "implement feature X, here's the spec." This means:
- No audit trail of what was assigned or why
- No visibility into progress
- Context lives in the prompt, not in the system
- Multiple agents can't coordinate
- The human can't redirect without interrupting

Tillr solves this by being the **single source of truth** for all agent work. Agents claim from a queue, implement from specs stored in tillr, and submit back through tillr. The human watches and steers from the tillr UI — changing priorities, adjusting scope, approving or rejecting results.

## The Implicit Tracking Principle

If we force all work through tillr, tracking is free. We don't need agents to "log their activity" — their activity IS the claims, updates, and submissions flowing through the system. The audit trail is a side effect of the workflow, not an extra step.

This is the key design insight: **don't ask agents to report — make reporting the only way to do work.**

## Design Principles

1. **Human QA is about judgment, not verification.** Show humans what changed and why. Let them approve or reject based on whether it's right, not whether it compiles.

2. **All context lives in tillr.** Feature specs, design docs, acceptance criteria, QA scripts — everything an agent needs to work. If it's not in tillr, it doesn't exist for agents.

3. **The queue is the interface.** Humans steer by changing priorities, scoping the queue, and setting acceptance criteria. Agents steer by claiming, implementing, and submitting.

4. **Visibility by default.** Every claim, every submission, every status change is visible. The human should be able to open tillr and immediately know: what's being worked on, what's done, what needs their attention.

5. **Workstream-centric.** The workstream page is the human's home base. Everything they need — what needs attention, what's in progress, what's done — is on one page. No navigating between six different views to understand the state of their work.

6. **Structure over natural language.** Workflow logic belongs in the tool, not in agent prompts. Status transitions, queue scoping, QA checks, ordering — all encoded in `tillr` CLI commands. An agent's prompt is "process the queue," not a paragraph of instructions. If you're tempted to add logic to an agent prompt, put it in the tool instead. Natural language is fragile; code is durable.

## Where We Are

We have the foundation: features, workstreams, status tracking, inline QA, progress bars, needs-attention summaries. What's next:

- **Agent orchestration** — the claim/submit workflow that makes agents first-class participants
- **Human QA redesign** — separate human-verifiable criteria from technical checks
- **Queue scoping** — let humans focus agents on specific workstreams or priorities
- **Better history** — make the audit trail visually clear and scannable
