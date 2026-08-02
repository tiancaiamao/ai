package model

// Capability represents a model's capability bitmask.
type Capability int

const (
	// CapabilityText is the baseline capability - all models support text.
	CapabilityText Capability = 1 << iota

	// CapabilityVision indicates the model supports image input (multimodal).
	CapabilityVision

	// CapabilityFunctionCalling indicates the model supports function/tool calling.
	CapabilityFunctionCalling
)

// Has checks if the model has a specific capability.
func (c Capability) Has(cap Capability) bool {
	return c&cap != 0
}

// SupportsVision returns true if the model supports image input.
func (c Capability) SupportsVision() bool {
	return c.Has(CapabilityVision)
}

// SupportsFunctionCalling returns true if the model supports function calling.
func (c Capability) SupportsFunctionCalling() bool {
	return c.Has(CapabilityFunctionCalling)
}
