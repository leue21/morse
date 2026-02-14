package plugin

import "context"

// Alert represents a notification to be sent.
type Alert struct {
	Title   string
	Message string
}

// Plugin is the interface all monitoring plugins must implement.
type Plugin interface {
	Name() string
	Check(ctx context.Context) ([]Alert, error)
	Describe() string
}
