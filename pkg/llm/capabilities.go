package llm

import (
	"github.com/tiancaiamao/ai/pkg/model"
)

// Capability is an alias for model.Capability.
type Capability = model.Capability

// Capability constants re-exported for convenience.
const (
	CapabilityText            = model.CapabilityText
	CapabilityVision          = model.CapabilityVision
	CapabilityFunctionCalling = model.CapabilityFunctionCalling
)
