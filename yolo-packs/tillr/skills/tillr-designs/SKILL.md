---
name: tillr-designs
description: Read tillr's design documentation from another repository — what tillr does today, what each design's status is, what it builds next, and which surface is stable enough to depend on. Use when your project consumes tillr (AutoQA, or anything that reads its store, binaries or reports), when you need to know whether a tillr behavior is built, queued or only proposed, or when you want to send tillr a request.
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

1. **The status index** (`docs/tillr/design-status.md`): one row per design, saying what it
   covers, whether it is built, partly built, queued, proposed or historical, and what tillr
   builds next.
2. **What tillr will change for AutoQA** (`docs/design/tillr-separable-parts.md`): which of a
   consumer's requests tillr will take, and the contract between tillr and a consumer.
3. The design rows that bear on your work, in the order the index lists them.

The owner has stated a direction for tillr's core and its landing that is not delivered yet.
When its document lands it becomes the second item above, and the index says so.

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

1. Write the request in **your own repository**, as a tracked document: one numbered request
   per entry, each with the tillr behavior you checked, the snapshot commit you read it at
   (from `snapshot.json`), and the date.
2. Tell the owner it exists. The owner relays it to tillr. Where the next round of requests
   should live is an open question,
   [`OQ-SP1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-separable-parts.md#OQ-SP1)
   in `tillr-separable-parts.md`. Until the owner
   rules on it, your own repository is the place.
3. tillr's answers come back in `tillr-separable-parts.md`. Section 1 gives each request's
   fate, Section 2 gives the contract, and its Open Questions list what waits on the owner.
   You see them in the next snapshot this pack delivers.
