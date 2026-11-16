---
title: Commit citations and upstream pins
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 087b4385b9208bf49b20d09fefe9564ea8618e96
covers:
  - tools/gate/checks/change_common.py
  - tools/gate/checks/citations.py
  - tools/gate/checks/citations.json
  - tools/gate/checks/change_checks_test.py
tags: [gate, citations, upstream, reference]
---

# Commit citations and upstream pins

**Status:** Source-verified 2026-11-16 against
`087b4385b9208bf49b20d09fefe9564ea8618e96`.

A document citing a repository commit as evidence must name history the reader
can reach from the checked head. An upstream pin instead identifies a dependency's
history, which need not be an ancestor of this repository's head. The citation
check distinguishes these cases through the local refs that contain the commit,
not through guesses about what the prose meant.

**Upstream pin**, as defined in [the original fix record](../pi/citation-upstream-pin-2026-11-02.md#2-the-rule),
means a cited commit reachable from a ref carrying upstream history. For this
check, that means membership in the explicit ref allowlist below, not proof of
who authored the commit. **Unlanded** means a repository change not contained in
the checked head; marking it explicitly makes that limitation visible to readers.

## What the check judges

`unreachable_citations` in
[`change_common.py`](../../tools/gate/checks/change_common.py) judges added lines
of tracked Markdown between a caller-supplied base and head. The
[`citations.py`](../../tools/gate/checks/citations.py) entry point calls `run`,
which validates both revisions as commits and requires base to be an ancestor of
head. The command is independent of the landing lock and any remembered gate
baseline:

```bash
python3 tools/gate/checks/citations.py BASE HEAD
```

The runner selects changed Markdown still present at the named head, excluding
paths under `.forks`, `node_modules`, or `.yolo`. It reads the committed document,
not a dirty working copy. Added-line discovery uses a zero-context diff without
rename detection. Fence membership is read from the whole committed document so
a newly added line inside an existing fence is not misclassified.

For each eligible line, the check considers lowercase hexadecimal tokens matching
`SHA_CITATION`. The decision is:

1. Lines inside a fenced block and lines carrying `[unlanded]` are exempt.
   A marker exempts the entire line, not just the closest token.
2. A token already present anywhere in that document's base version is exempt.
   Reflowing historical prose does not make that same token a new claim.
3. Tokens that cannot resolve locally to a commit are not judged. A dependency
   SHA and a hexadecimal identifier can look alike; failure to resolve is not
   evidence of an unlanded repository commit.
4. A resolved commit passes if it is an ancestor of the named head, or if any
   containing ref matches `UPSTREAM_REF`.
5. Otherwise, the check reports the document, line, token and failed readings,
   and asks for `[unlanded]` when the citation is intentional.

This is a change-only check, not certification that every historical citation is
valid. Passing an unresolved token is also not proof that the cited upstream
commit exists somewhere else.

## Allowed containing refs

`reachable_from_upstream` runs `git for-each-ref --contains` for the resolved
commit, then matches the returned names against `UPSTREAM_REF`.

| Accepted prefix | Meaning |
| :--- | :--- |
| `refs/heads/patch/` | Local delivery branches carrying upstream history |
| `refs/published/patch/` | Recorded published delivery branches |
| `refs/remotes/<remote>/patch/` | Remote-tracking delivery branches |
| `refs/remotes/upstream/` | Tracking refs of the explicitly named upstream remote |
| `refs/tags/` | Tags, including release pins |

The match is based on ref names and reachability. It does not fetch, validate
remote ownership, or require an annotated or signed release tag. Any tag that
contains the commit satisfies this rule. A future upstream-bearing ref outside
these prefixes does not qualify until the allowlist changes.

A local lane branch alone does not qualify. Neither does an ordinary
remote-tracking lane branch under origin: publishing unfinished work does not
make it upstream. When such a commit is intentional historical evidence, mark
the citation line `[unlanded]`; do not relabel a lane ref to obtain an exemption.

## Failure and verification

The entry point exits zero when it finds no refused citations, one when it has
findings, and four when it cannot judge, such as missing revisions or a base that
is not an ancestor. A finding is a refusal, not a request to fetch a missing
upstream. An unavailable judgment is not a clean result.

The permanent `test_citations_markers_fences_and_upstream_pins` in
[`change_checks_test.py`](../../tools/gate/checks/change_checks_test.py) checks a
lane-only commit, explicit markers, fenced specimens, contained repository
commits and an upstream-tracking ref. Re-verification should inspect the regex
as well as that test: its acceptance of all tags is broader than only release
tags, and the test does not exercise every allowed prefix.

## Current values and historical location

| Value | At the verification stamp | Owner |
| :--- | :--- | :--- |
| Candidate token | 7–40 lowercase hexadecimal characters, bounded against letters and digits | `SHA_CITATION` |
| Explicit exemption | `[unlanded]` anywhere on the line | `UNLANDED_MARKER` |
| Upstream classification | The containing-ref prefixes above | `UPSTREAM_REF` |
| Gate declaration | Caller-owned base/head change check | [citation declaration](../../tools/gate/checks/citations.json) |

The [fix record](../pi/citation-upstream-pin-2026-11-02.md) describes the original
implementation in [the old fork checker](../../tools/fork-check.py). The rule has
moved to the change-check entry point and shared module named above. The record
retains its original measurements and location as history; it is not the current
execution guide.
