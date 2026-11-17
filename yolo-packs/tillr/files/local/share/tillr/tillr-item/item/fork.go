package item

import "strings"

// ForkCheckoutPath reads a fence entry for a checkout staging materializes. The filed
// lane is a placeholder: staging rebinds it to the dispatched lane, keeping the suffix.
// Noncanonical paths are not checkout declarations and receive normal path validation.
func ForkCheckoutPath(raw string) (lane, fork, suffix string, ok bool) {
	rest, ok := strings.CutPrefix(raw, ".forks/lanes/")
	if !ok {
		return "", "", "", false
	}
	lane, rest, ok = strings.Cut(rest, "/")
	if !ok || lane == "" || lane == "." || lane == ".." {
		return "", "", "", false
	}
	fork, suffix, hasSuffix := strings.Cut(rest, "/")
	if fork == "" || fork == "." || fork == ".." {
		return "", "", "", false
	}
	if hasSuffix {
		suffix = "/" + suffix
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "." || part == ".." || strings.Contains(part, "\\") {
			return "", "", "", false
		}
	}
	return lane, fork, suffix, true
}
