//go:build !linux

package store

import "errors"

// otherProcessesHolding cannot tell, outside Linux, which processes hold the store's locks,
// so TruncateLog refuses there rather than guess.
func otherProcessesHolding(path string) ([]int, error) {
	return nil, errors.New("only Linux's /proc/locks is read for the store's other holders")
}
