// Package cli is the post-reset Cobra CLI for tillr.
//
// Surface: tillr init, tillr serve, tillr feature {add,list,show},
// tillr comment. Everything else from pre-reset is in
// archive/pre-reset for git archaeology — see docs/reset.md.
package cli

// Exit codes for CLI errors.
const (
	ExitSuccess   = 0
	ExitUserError = 1
	ExitSysError  = 2
)
