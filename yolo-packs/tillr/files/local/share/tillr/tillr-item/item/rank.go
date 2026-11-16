package item

import "strings"

// RankAlphabet is the character set a rank is spelled in, listed in the order string
// comparison puts its characters: the sparse lexical rank alphabet the capacity queue
// already carries (`QUEUE_RANK_DIGITS` in pi-subagents' `src/runs/background/queue-rank.ts`,
// itself the pile's `PILE_RANK_DIGITS` in `work-control.ts`). A rank is an opaque token over
// these 36 characters compared as a plain string — there is deliberately no numeric
// priority beside it, because two orderings of one queue is how a queue ends up with an
// order nobody can explain.
//
// The alphabet is what makes a rank *sparse*: a token may be any length, so a gap between
// two ranks is what makes inserting between them cheap — `a`, then `am` between `a` and `b`
// — and no rank is renumbered to make room. Ranks are stored and compared as the strings
// they are; nothing here generates one, because the value is the caller's decision and
// this utility only refuses one it cannot order.
const RankAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// checkRank refuses a rank spelled outside the alphabet. The refusal is what keeps the
// store's order the alphabet's order: an uppercase letter sorts before every lowercase
// one, so a rank carrying one would take a place in the queue that no rank in the alphabet
// describes. An empty rank is not a rank at all — it is the unranked item, which sorts
// after every ranked one — so it is legal.
func checkRank(itemID, rank string) error {
	for offset, character := range rank {
		if strings.ContainsRune(RankAlphabet, character) {
			continue
		}
		return Rejectionf("rank-alphabet", itemID,
			"rank %q carries %q at offset %d: a rank is a string over %q, the alphabet the queue's ranks are compared in",
			rank, string(character), offset, RankAlphabet)
	}
	return nil
}
