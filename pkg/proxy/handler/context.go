package handler

import (
	"context"
	"time"
)

// Context keys for passing step information
type contextKey string

const (
	stepInfoKey contextKey = "stepInfo"
)

// StepInfo holds step information for query processing
type StepInfo struct {
	OriginalStep   time.Duration // Step requested by client (Grafana)
	ConfiguredStep time.Duration // Step forced to backends
}

// WithStepInfo adds step information to the context
func WithStepInfo(ctx context.Context, info StepInfo) context.Context {
	return context.WithValue(ctx, stepInfoKey, info)
}

// GetStepInfo retrieves step information from the context
func GetStepInfo(ctx context.Context) (StepInfo, bool) {
	info, ok := ctx.Value(stepInfoKey).(StepInfo)
	return info, ok
}
