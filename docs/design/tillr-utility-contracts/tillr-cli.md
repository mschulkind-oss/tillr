---
title: "Contract: tillr-cli"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags: [tillr, contract, cli]
summary: "What tillr-cli provides and expects: the one command `tillr <noun>`, eight nouns it hands to another program, one it answers itself, and three exit codes of its own. It expects one program per dispatched noun on the path, and the list of programs is typed in four places that do not agree."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-cli

**Status:** Nothing here is built: `tools/tillr-cli` has no `contract/` directory. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it. The surface below was checked against `tools/tillr-cli` on 2026-11-24.

**What it is.** One Go program at `tools/tillr-cli`, installed as `tillr`. It finds the program `tillr-<noun>` on the path and replaces itself with it, passing every argument after the noun untouched. Its [README](../../../tools/tillr-cli/README.md) describes the same dispatch for a person running it.

## 1. What it provides

### 1.1 Faces

| Command | What it does | Exit |
| :--- | :--- | :--- |
| `tillr <noun> …` | Runs `tillr-<noun> …`; reads nothing after the noun | the child's |
| `tillr notify serve` | Runs the local wake-hint relay inside this program; no other program is started | its own |
| `tillr --help`, `tillr -h` (alone) | Prints the nouns | 0 |
| `tillr` with no noun, or a word that is no noun | Prints the nouns on standard error | 2 |
| a noun whose program is not on the path, or cannot be started | Prints which program is missing | 4 |
| any other error | — | 5 |

### 1.2 Go packages

None. It is a `main` package.

### 1.3 Records, events and tables

None.

### 1.4 Files and environment

It reads `PATH` and passes its whole environment to the child.

### 1.5 Names others match on

**The nouns**, in order: `store`, `item`, `land`, `drain`, `run`, `gate`, `view`, `watch`, `notify`. The first eight are dispatched to a program of that name; `notify` is answered by this program. The three scripts (`tillr-loop`, `tillr-trace`, `tillr-roll`) are not nouns and are reached by their own names.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| A program named `tillr-store` | [tillr-store](./tillr-store.md) | subprocess | `main.go` |
| A program named `tillr-item` | [tillr-item](./tillr-item.md) | subprocess | `main.go` |
| A program named `tillr-land` | [tillr-land](./tillr-land.md) | subprocess | `main.go` |
| A program named `tillr-drain` | [tillr-drain](./tillr-drain.md) | subprocess | `main.go` |
| A program named `tillr-run` | [tillr-run](./tillr-run.md) | subprocess | `main.go` |
| A program named `tillr-gate` | [tillr-gate](./tillr-gate.md) | subprocess | `main.go` |
| A program named `tillr-view` | [tillr-view](./tillr-view.md) | subprocess | `main.go` |
| A program named `tillr-watch` | [tillr-watch](./tillr-watch.md) | subprocess | `main.go` |
| The exit codes 0, 2, 4 and 5 | [tillr](./tillr.md) | Go import | `main.go` |
| The wake-hint relay of the library's `notify` package | [tillr](./tillr.md) | Go import | `notify.go` |

It reads no output of any child: after a successful start it is not running.

## 3. Contract and private

**Contract:** the eight dispatched nouns; that arguments, standard input, standard output and the exit code pass through unchanged; exit 2 for a missing or unknown noun; exit 4 for a program that is not on the path.

**Private:** the usage and refusal sentences.

## 4. How the contract is pinned

In `tools/tillr-cli`, in its fast suite.

- **Exists today.** `TestDispatchPassesArgvStdioAndExitCode`, `TestWrapperReadsNothingAfterTheNoun`, `TestUnknownNounIsRefusedNamingTheNouns`, `TestNoNounIsRefusedNamingTheNouns`, `TestMissingBinaryIsAnExternalFailure` and `TestHelpNamesTheNouns`.
- **To add.** The nouns become a name-set file, and a test compares it with the `nouns` variable. The two refusal tests compare the exit code and that each noun appears, not the sentence.
- **The list of programs.** The library's contract check compares the noun file with the utilities that have a contract directory and a `main` package, so a new program without a noun, or a noun without a program, fails there.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- RI-44: the list of programs is typed here, in the drain's `schema_roll.go`, in the roller's `MODULES` and in the pack mirror, and the lists differ. After slice 14 there is one list, and the other three read it.
