# pkg/model

Model capability definitions and types.

## Overview

This package provides a capability bitmask system for LLM models. It separates capability definitions from other packages to avoid circular dependencies.

## Capability Bitmask

```go
type Capability int

const (
    CapabilityText            Capability = 1 << iota // 0x01 - baseline, all models support text
    CapabilityVision                                  // 0x02 - supports image_url content
    CapabilityFunctionCalling                         // 0x04 - supports function calling
)
```

The bitmask approach allows combining multiple capabilities:
- `CapabilityText | CapabilityVision` = 0x03 (supports both text and images)
- `CapabilityText | CapabilityFunctionCalling` = 0x05 (supports text and function calling)
- `CapabilityText | CapabilityVision | CapabilityFunctionCalling` = 0x07 (supports all)

## Methods

```go
func (c Capability) Has(other Capability) bool
func (c Capability) SupportsVision() bool
func (c Capability) SupportsFunctionCalling() bool
```

### Has

Checks if the capability bitmask includes the specified capability:

```go
caps := CapabilityVision | CapabilityText

caps.Has(CapabilityVision)   // true
caps.Has(CapabilityText)     // true
caps.Has(CapabilityFunctionCalling) // false
```

### SupportsVision

Convenience method to check vision support:

```go
caps := CapabilityVision | CapabilityText
caps.SupportsVision() // true
```

### SupportsFunctionCalling

Convenience method to check function calling support:

```go
caps := CapabilityFunctionCalling | CapabilityText
caps.SupportsFunctionCalling() // true
```

## Usage

### Model Configuration

In `pkg/config/models.go`, capabilities are parsed from model JSON configuration:

```go
type modelConfig struct {
    Input         []string `json:"input,omitempty"`        // ["text", "image"]
    Capabilities  []string `json:"capabilities,omitempty"` // ["vision"]
}

// Parse capabilities from input types and explicit declarations
specs[i].Capabilities = parseCapabilities(model.Capabilities, model.Input)
```

### Message Filtering

In `pkg/llm/filter.go`, messages are filtered based on model capabilities:

```go
// Remove image_url content for text-only models
filtered := FilterMessagesForCapability(messages, model.Capabilities)

// Check what would be filtered
unsupported := DetectUnsupportedContent(messages, model.Capabilities)
if unsupported != "" {
    // Log warning: "2 images would be filtered for model X"
}
```

## Integration Points

- `pkg/config` — `ModelSpec.Capabilities` field and `parseCapabilities()`
- `pkg/llm` — `Model.Capabilities` field and `FilterMessagesForCapability()`
- `pkg/agent` — capability filtering in `llm_stream.go`