package models

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/pkg/config"
)

type row struct {
	provider string
	id       string
	ctx      string
	maxOut   string
	thinking string
	images   string
}

func ModelsSubcommand() {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	providerFlag := fs.String("provider", "", "Filter by provider name")
	fs.Parse(os.Args[1:])

	args := fs.Args()
	var filter string
	if len(args) > 0 {
		filter = args[0]
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	modelsPath := filepath.Join(home, ".ai", "models.json")
	if override := strings.TrimSpace(os.Getenv("AI_MODELS_PATH")); override != "" {
		modelsPath = override
	}

	specs, err := config.LoadModelSpecs(modelsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading models: %v\n", err)
		os.Exit(1)
	}
	// Only show models whose providers have API keys configured.
	specs = config.FilterModelSpecsWithKeys(specs)
	if len(specs) == 0 {
		fmt.Println("no models available (missing API keys?)")
		return
	}

	// Sort by provider, then by model ID.
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Provider != specs[j].Provider {
			return specs[i].Provider < specs[j].Provider
		}
		return specs[i].ID < specs[j].ID
	})

	filterLower := strings.ToLower(strings.TrimSpace(filter))
	providerFilter := strings.TrimSpace(*providerFlag)

	rows := make([]row, 0)
	for _, spec := range specs {
		// Apply --provider filter.
		if providerFilter != "" && !strings.EqualFold(spec.Provider, providerFilter) {
			continue
		}
		// Apply positional filter (fuzzy match against provider+id).
		if filterLower != "" {
			haystack := strings.ToLower(spec.Provider + " " + spec.ID)
			if !strings.Contains(haystack, filterLower) {
				continue
			}
		}

		rows = append(rows, row{
			provider: spec.Provider,
			id:       spec.ID,
			ctx:      formatTokenCount(spec.ContextWindow),
			maxOut:   formatTokenCount(spec.MaxTokens),
			thinking: boolStr(spec.Reasoning),
			images:   inputHas(spec.Input, "image"),
		})
	}

	if len(rows) == 0 {
		if filter != "" {
			fmt.Printf("no models matching %q\n", filter)
		} else if providerFilter != "" {
			fmt.Printf("no models for provider %q\n", providerFilter)
		} else {
			fmt.Println("no models available")
		}
		return
	}

	// Print table.
	printModelTable(rows)
}

func printModelTable(rows []row) {
	// Column widths.
	type col struct {
		header string
		value  func(row) string
	}
	cols := []col{
		{"provider", func(r row) string { return r.provider }},
		{"model", func(r row) string { return r.id }},
		{"context", func(r row) string { return r.ctx }},
		{"max-out", func(r row) string { return r.maxOut }},
		{"thinking", func(r row) string { return r.thinking }},
		{"images", func(r row) string { return r.images }},
	}

	widths := make([]int, len(cols))
	for i, c := range cols {
		w := len(c.header)
		for _, r := range rows {
			if l := len(c.value(r)); l > w {
				w = l
			}
		}
		widths[i] = w
	}

	// Header.
	var headerParts []string
	for i, c := range cols {
		headerParts = append(headerParts, pad(c.header, widths[i]))
	}
	fmt.Println(strings.Join(headerParts, "  "))

	// Rows.
	for _, r := range rows {
		var parts []string
		for i, c := range cols {
			parts = append(parts, pad(c.value(r), widths[i]))
		}
		fmt.Println(strings.Join(parts, "  "))
	}
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func formatTokenCount(count int) string {
	if count <= 0 {
		return "-"
	}
	if count >= 1_000_000 {
		if count%1_000_000 == 0 {
			return fmt.Sprintf("%dM", count/1_000_000)
		}
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	}
	if count >= 1_000 {
		if count%1_000 == 0 {
			return fmt.Sprintf("%dK", count/1_000)
		}
		return fmt.Sprintf("%.1fK", float64(count)/1_000)
	}
	return fmt.Sprintf("%d", count)
}

func boolStr(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func inputHas(input []string, target string) string {
	for _, v := range input {
		if v == target {
			return "yes"
		}
	}
	return "no"
}
