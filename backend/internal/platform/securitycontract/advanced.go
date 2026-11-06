// Package securitycontract defines the optional advanced security inspection
// boundary shared by the open-source core and proprietary implementations.
package securitycontract

import "context"

// AdvancedInspectField is one decoded inspection field supplied to an optional
// advanced detector. Values are request-local and must not be retained.
type AdvancedInspectField struct {
	Path   string
	Source string
	Kind   string
	Value  []byte
}

// AdvancedInspectRequest contains the decoded fields for one content scan.
type AdvancedInspectRequest struct {
	Kind   string
	Fields []AdvancedInspectField
}

// AdvancedInspectHit identifies one match relative to its field value.
type AdvancedInspectHit struct {
	FieldIndex int
	Start      int
	End        int
	Reason     string
	RuleName   string
	Action     string
}

// AdvancedInspectResult contains optional findings and observability signals.
// Signals are weak hints and must never directly change routing or blocking.
type AdvancedInspectResult struct {
	Hits    []AdvancedInspectHit
	Signals []string
	Decoded bool
}

// AdvancedDetector is implemented only by the proprietary advanced package.
// The open-source build injects NoopAdvancedDetector.
type AdvancedDetector interface {
	Inspect(context.Context, AdvancedInspectRequest) (AdvancedInspectResult, error)
}

// NoopAdvancedDetector keeps the open-source pipeline fully functional without
// importing or shipping private detection content.
type NoopAdvancedDetector struct{}

// Inspect intentionally returns no findings.
func (NoopAdvancedDetector) Inspect(context.Context, AdvancedInspectRequest) (AdvancedInspectResult, error) {
	return AdvancedInspectResult{}, nil
}
