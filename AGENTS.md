# tillr agent guide

**The whale** ([defined in the authority reference](./docs/reference/authority-not-approval.md#terms)) is the coordinating AI session that operates tillr on the owner's behalf — steering the drain, filing items, and clearing faults — distinct from a lane worker, which builds one item in one lane.

## Landing a lane: verified candidates

Before changing landing, the gate, the queue or the repair path, check the change against
[`docs/design/landing-principles.md`](./docs/design/landing-principles.md) — the library of durable
rulings, each with the measurement that produced it and what enforces it today. Every entry says
what is *not* enforced yet, so a proposal can be tested against what is settled rather than against
what is convenient. The rule the set keeps proving: a mechanism whose cost rises with trunk
activity is pointed the wrong way.

Two landing paths exist, and their verification rules are distinct rather than contradictory:

- **Automated squash lander ([`tools/tillr-land`](./tools/tillr-land/)):** `Land`
  ([`tools/tillr-land/land/landing.go`](./tools/tillr-land/land/landing.go)) checks that the
  `gate.verdict` binds `(base_sha, head_sha^{tree})`, runs `Trunk.Squash`
  ([`tools/tillr-land/land/trunk.go`](./tools/tillr-land/land/trunk.go)) to three-way merge
  `base_sha..head_sha` onto `trunk_before`, and records both `gated_tree` and `landed_tree` on
  `landings`. It **never compares patch identity** (`git patch-id`), because a replayed diff —
  and `gated_tree` differing from `landed_tree` — is the normal outcome of a concurrent landing
  ([`docs/design/landing-principles.md`](./docs/design/landing-principles.md)
  [§2](./docs/design/landing-principles.md#2-re-application-preserves-the-change-not-the-patch)
  and [§3](./docs/design/landing-principles.md#3-gate-what-will-be-the-tip-once)).
- **Manual and agent cherry-pick path:** When a human or a dedicated landing lane cherry-picks a
  lane's commit onto the trunk outside `tools/tillr-land`, the cherry-pick rewrites the sha with
  no `landings` row binding a verdict to the tree. That manual and agent cherry-pick path is what
  the `git patch-id --stable` and byte-identical-blob comparison governs: verify the candidate
  **before** the cherry-pick and then compare the landed artifact to that verified candidate by
  `git patch-id --stable` or byte-identical blobs — never by asserting the two are equal.
- **Authorship follows the landing, not the lane:** a manual or agent cherry-pick (outside
  `tools/tillr-land`) is followed by the same author normalization — re-author the landed commit
  to the identity the repository's git config states, keeping the candidate's author date with
  `GIT_AUTHOR_DATE` — so no lane-branch identity reaches the trunk. READ that identity
  (`git config user.name`, `git config user.email`); never type one, for any reason. An
  environment with no identity configured is a setup fault to fix, not an address to invent.
  The automated squash lander already does this:
  [`tools/tillr-land`](./tools/tillr-land/) (`Trunk.LandingIdentity` and `Trunk.Squash` in
  [`tools/tillr-land/land/trunk.go`](./tools/tillr-land/land/trunk.go)) reads `user.name` and
  `user.email` from non-lane-writable config scopes, refuses to land without them or when a
  repo-local or worktree config overrides them, and stamps the squashed commit with that
  identity.
- **The two-identity history filter is this repository's policy, not part of the tillr programs.** For each commit
  in the checked range, the author and committer must each match either the owner's personal
  or professional name/email pair in the
  [tracked list](./tools/gate/allowed-commit-identities.json). The
  [standalone check](./tools/tillr/cmd/git-identity) and the live gate apply the same list,
  regardless of which identity the machine uses. Local and worktree identity overrides are
  still refused. When creating a commit, read the identity from Git config and never type it.


## Documents: state the evidence, never cite scratch

A doc must not cite a path in the durable scratch directory as evidence of what
happened. That scratch is retention-managed and outside the project, so the citation
expires and a reader cannot tell an expired one from a claim that was never true.
State the command, the result and the date instead, and copy an artifact into the
repo beside the doc if it must be kept. These are the two spellings:

```text
$YOLO_DURABLE_DIR/...      .yolo/durable/...
```

The gate's `scratch` check ([`tools/gate/checks/scratch.json`](./tools/gate/checks/scratch.json))
fails a tracked Markdown file that cites one in prose or inline code; a command in a fenced
block is a specimen, not a citation, and is not read. A documented *location* — a tool's
intended output directory — is the one exception, each named in the check's allowlist with the
reason it stays.

**A commit sha is evidence only when HEAD contains it.** A document that cites a commit
as proof — "the suite counts are in commit X" — claims a reader can go and read that
commit. A sha that lived only on a lane branch, or that a history rewrite orphaned, cannot
be checked, and a reader cannot tell it from a claim that was never true. The gate's
`citations` check ([`tools/gate/checks/citations.json`](./tools/gate/checks/citations.json))
refuses a line added to tracked Markdown that cites one (`git merge-base --is-ancestor
<sha> HEAD` says no). When the unlanded citation is the point — a record of what a lane
did — mark the line `[unlanded]`; that marker is the reader-visible form, and a grep finds
it. The guard judges added lines only.

**A citation into the tree is an anchor or a quoted line, never a bare line number.**
A number rots the moment anything above it moves: one relocation makes every citation
below it stale at once. Cite a heading anchor, a question anchor, a function name or the
quoted line. A **negative claim carries its command and its scope**: `rg 'x'
<where>` returning nothing is evidence; "x is not in the tree" is not.

**Name the file; a directory check skips what is hidden.** `uvx vantage-check <dir>` skips
every entry whose name starts with `.` as it walks (`src/core/discover.ts`, `walk()`), so a
green there says nothing about a document under a hidden subdirectory — name the file, and
keep a positive control at the real repo-relative path.

**A letter-suffixed section is referenced by a labelled link, never a section sign.** Measured on
`vantage-check` 0.10.0 against a document holding both `## 4.` and `## 4a.`: a section sign before
a letter is read as the plain number, so every section-sign spelling is refused and only the
labelled link is accepted.

```text
§4a                                           rejected: read as §4, refused as unlinked
[§4a](#4a-the-letter-suffixed-section)        rejected: §4 compared to the plain #4 heading
[§4](#4a-the-letter-suffixed-section)         rejected: same finding, same reason
[Section 4a](#4a-the-letter-suffixed-section) accepted — the only spelling that works
```

That is the external checker's behaviour rather than a house preference, so a letter-suffixed
section gets the labelled link, and a section-sign label is written only for a plain-numbered
section. The collision is what bites: with no plain `#4` heading in the document the section-sign
form passes, so a green document breaks when it later gains a `4`.


## Operating the loop here

- **Session directory and rulings file:** Not fixed in this repository.
- **Lanes:** [`.tillr.toml`](./.tillr.toml) reserves `2` express lanes (`[lanes] express = 2`) and sets no ordinary lane count (`tillr-drain loop` defaults `--lanes` to `2` on a fresh start, overridden by `--lanes` or a saved `ordinary_lanes` scale target).
- **Backend policy:** The backend policy in force is passed with `--policy` from outside the repository; the tracked [`config/tillr/backend-policy.json`](./config/tillr/backend-policy.json) is only an example with placeholder model names.
- **Watch rules:** [`config/tillr/watch`](./config/tillr/watch).
