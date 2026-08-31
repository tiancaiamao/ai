package modelselect

import (
	"sort"
	"strings"
)

// Keys represents normalized model identity fields used for selection and sorting.
type Keys struct {
	Provider string
	ID       string
	Name     string
}

// KeyExtractor extracts model identity fields from a value.
type KeyExtractor[T any] func(item T) Keys

// SortByModelKey sorts models by provider, id, then name (case-insensitive).
func SortByModelKey[T any](items []T, extract KeyExtractor[T]) {
	sort.Slice(items, func(i, j int) bool {
		keyI := normalize(extract(items[i]))
		keyJ := normalize(extract(items[j]))
		if keyI.Provider != keyJ.Provider {
			return keyI.Provider < keyJ.Provider
		}
		if keyI.ID != keyJ.ID {
			return keyI.ID < keyJ.ID
		}
		return keyI.Name < keyJ.Name
	})
}

func normalize(key Keys) Keys {
	return Keys{
		Provider: strings.ToLower(strings.TrimSpace(key.Provider)),
		ID:       strings.ToLower(strings.TrimSpace(key.ID)),
		Name:     strings.ToLower(strings.TrimSpace(key.Name)),
	}
}
