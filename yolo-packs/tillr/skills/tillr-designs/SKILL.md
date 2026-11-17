---
name: tillr-designs
description: Read tillr's design documentation from another repository — what tillr does today, what each design's status is, what it builds next, and which surface is stable enough to depend on. Use when your project consumes tillr (the Product QA pipeline, or anything that reads its store, binaries or reports), when you need to know whether a tillr behavior is built, queued or only proposed, or when you want to send tillr a request.
---

# Reading tillr's designs from another repository

tillr is the owner's work-queue toolset: a store of queue items, and binaries that stage
each item into its own worktree, run an agent on it, judge it against its criteria and land
it. Its designs live in the owner's repository, not in yours. This pack delivers a
**dated snapshot** of them, so you can read them without that repository.

## Where the files are

- `~/.local/share/tillr/docs/tillr/design-status.md` is the **status index**. Start here.
- `~/.local/share/tillr/docs/design/` holds every design the index marks `shipped`.
- `~/.local/share/tillr/<module>/README.md` is each module's reference, delivered with its
  source.
- `~/.local/share/tillr/docs/snapshot.json` names the commit and date of the snapshot and
  every document in it.

Every delivered document opens with a banner that names its source file and commit. The
source is authoritative and may be newer. A link to a document that was not delivered points
at the repository on GitHub. The queue item directives in a copy are made inert, so a copy is
never read as a second declaration of an item.

## Reading order

The paths below are under `~/.local/share/tillr/`.

1. `docs/tillr/design-status.md`, its first section, "Read this first": what tillr is today, the
   owner's direction for it, and how to read a status.
2. `docs/design/tillr-core-and-git-landing.md`, Sections 1, 4, 5, 6 and 8: the owner's stated
   direction for tillr. tillr's code today is machinery for landing changes in a Git repository.
   The direction is to separate it into general machinery of queues and graphs ("the core") and
   one "definition" per use on top, the first being `git-landing`. Those are placeholder words;
   the owner has not chosen names. On 2026-11-17 the owner said to build it, and Section 8 lists
   the working choices the build rests on, each still his to confirm or change.
3. `docs/design/tillr-definitions-plan.md`: the items being built, in order, and for each one
   what the Product QA pipeline can try once it lands. Its last item is a worked definition that
   lands nothing, with a README you can run.
4. `docs/design/tillr-separable-parts.md`: first "For the Product QA project's agent", then
   Section 7 (each request's fate and the item that builds it), Section 2 (the contract), then
   Section 1.3 (what tillr will not change). `docs/design/tillr-product-qa-requests.md` is the
   pipeline's second-round reply as received.
5. Optional background, how work enters the Git-landing use: `docs/design/tillr-intake-paths.md`.
6. Reply as the next section says.

"Product QA" is a product bug-finding pipeline. `docs/design/tillr-autoqa.md` is a
different thing: tillr's own design for exploratory testing.

Read any other row of the status index when your work touches it.

## How to read a status

Trust the index's status over a document's own status line: the index is checked against the
code and the queue on the date in its first lines, and a document's line can lag. A design
that is **proposed** is waiting on the owner's rulings, named by question ids that
start with `OQ-`, and nothing in it is built. **Queued** means its work is filed as queue items, named
by id. Do not build on a behavior a design only proposes.

## What is stable

Depend only on the surface named in Section 2.1, "The stable surface", of
`tillr-separable-parts.md`. Everything Section 2.2 calls internal may change on any day,
because the drain lands changes to tillr continuously. Section 2.3 says how a breaking change
to the stable surface is announced.

## Sending tillr a request

Do not edit the delivered copies. They are replaced whole on the next snapshot, and nothing
reads them back.

1. Write the reply as a tracked document in your own repository and tell the owner where it
   is; the owner relays it. tillr records each reply as received in the owner's repository
   repository, as `docs/design/tillr-product-qa-requests.md` was for the second round.
2. Make it one numbered request per entry, each with the tillr behavior you checked, the
   snapshot commit you read it at (from `docs/snapshot.json`), and the date. For the direction
   document, react to its Sections 4 and 5 with one concrete case from your own project for each
   extension point you need.
3. tillr's answers come back in `tillr-separable-parts.md`. Section 7 gives each request's fate,
   with the item that builds it, and the items themselves are in the build plan; Section 1 gave
   the first round's. Section 2 gives the contract, and its Open Questions list what waits on the
   owner. Section 2.4 says how requests and answers travel. Tell the owner which items you tried,
   against which snapshot, and what you saw; that is the next round. You see the answers in the next snapshot this pack
   delivers.
