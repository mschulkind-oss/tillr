// Package event holds the append primitive every utility writes its own state
// transitions through, and the registry of the kinds it may append.
package event

import (
	"fmt"
	"sort"
	"sync"
)

// Kind is one registered event kind and the utility that owns it.
type Kind struct {
	Name  string
	Owner string
}

var (
	mu       sync.RWMutex
	registry = map[string]Kind{}
)

// Register adds a kind to the registry. The defect the registry exists to catch is
// two writers of one state, so a kind already owned by a *different* utility is
// refused rather than overwritten. The same owner registering its own kind again is
// a no-op: a package linked twice, or a test run with -count=2, is not a defect and
// must not become an import-time panic.
func Register(kind, owner string) error {
	if kind == "" || owner == "" {
		return fmt.Errorf("event: a kind needs a name and an owner")
	}
	mu.Lock()
	defer mu.Unlock()
	if existing, ok := registry[kind]; ok {
		if existing.Owner == owner {
			return nil
		}
		return fmt.Errorf("event: kind %q is already owned by %q", kind, existing.Owner)
	}
	registry[kind] = Kind{Name: kind, Owner: owner}
	return nil
}

// Lookup answers one registered kind, or an error for a kind nothing registered.
func Lookup(kind string) (Kind, error) {
	mu.RLock()
	defer mu.RUnlock()
	found, ok := registry[kind]
	if !ok {
		return Kind{}, fmt.Errorf("event: kind %q is not registered", kind)
	}
	return found, nil
}

// Kinds answers the registry in name order, so a caller rendering it is stable.
func Kinds() []Kind {
	mu.RLock()
	defer mu.RUnlock()
	all := make([]Kind, 0, len(registry))
	for _, kind := range registry {
		all = append(all, kind)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}
