package modelselect

import (
	"errors"
	"fmt"
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

var (
	// ErrNotFound indicates no model matched a selector.
	ErrNotFound = errors.New("model not found")
	// ErrAmbiguous indicates a selector matches multiple models.
	ErrAmbiguous = errors.New("model selector is ambiguous")
)

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

// SelectByQuery resolves a model selector by exact match first, then prefix match.
func SelectByQuery[T any](items []T, query string, extract KeyExtractor[T]) (T, error) {
	var zero T
	query = strings.TrimSpace(query)
	if query == "" {
		return zero, fmt.Errorf("model id is empty")
	}
	if len(items) == 0 {
		return zero, fmt.Errorf("no models configured")
	}

	if idx := findExactMatchIndex(items, query, extract); idx >= 0 {
		return items[idx], nil
	}

	matches := findPrefixMatches(items, query, extract)
	switch len(matches) {
	case 0:
		return zero, fmt.Errorf("%w: %s", ErrNotFound, query)
	case 1:
		return items[matches[0]], nil
	default:
		return zero, fmt.Errorf("%w: %s (matches: %s)", ErrAmbiguous, query, formatMatches(items, matches, extract))
	}
}

func findExactMatchIndex[T any](items []T, query string, extract KeyExtractor[T]) int {
	queryLower := strings.ToLower(query)
	for i := range items {
		key := normalize(extract(items[i]))
		if key.ID == queryLower {
			return i
		}
	}
	for i := range items {
		key := normalize(extract(items[i]))
		if key.Name != "" && key.Name == queryLower {
			return i
		}
	}
	return -1
}

func findPrefixMatches[T any](items []T, query string, extract KeyExtractor[T]) []int {
	queryLower := strings.ToLower(query)
	matches := make([]int, 0)
	seen := make(map[string]struct{}, len(items))

	for i := range items {
		key := normalize(extract(items[i]))
		if strings.HasPrefix(key.ID, queryLower) {
			identity := key.Provider + "\x00" + key.ID
			if _, ok := seen[identity]; ok {
				continue
			}
			seen[identity] = struct{}{}
			matches = append(matches, i)
		}
	}

	for i := range items {
		key := normalize(extract(items[i]))
		if key.Name == "" || !strings.HasPrefix(key.Name, queryLower) {
			continue
		}
		identity := key.Provider + "\x00" + key.ID
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		matches = append(matches, i)
	}

	return matches
}

func normalize(key Keys) Keys {
	return Keys{
		Provider: strings.ToLower(strings.TrimSpace(key.Provider)),
		ID:       strings.ToLower(strings.TrimSpace(key.ID)),
		Name:     strings.ToLower(strings.TrimSpace(key.Name)),
	}
}

func formatMatches[T any](items []T, indices []int, extract KeyExtractor[T]) string {
	parts := make([]string, 0, len(indices))
	for _, idx := range indices {
		key := extract(items[idx])
		provider := strings.TrimSpace(key.Provider)
		id := strings.TrimSpace(key.ID)
		name := strings.TrimSpace(key.Name)
		ref := fmt.Sprintf("%s/%s", provider, id)
		if name != "" && !strings.EqualFold(name, id) {
			parts = append(parts, fmt.Sprintf("%s (%s)", ref, name))
			continue
		}
		parts = append(parts, ref)
	}
	return strings.Join(parts, ", ")
}
