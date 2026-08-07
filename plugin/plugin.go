package plugin

import (
	"context"
	"fmt"
)

// Severity is how much the reader should care, described from the checker's
// side. It deliberately does not name a delivery behaviour: "silent" is a
// Telegram concept, and a second channel would express the same three levels
// differently. Each channel maps these itself.
type Severity string

const (
	// SeverityInfo is a routine fact worth recording but not worth interrupting
	// anyone for. Delivered without a notification.
	SeverityInfo Severity = "info"
	// SeverityWarning is something to look at before it becomes a problem.
	SeverityWarning Severity = "warning"
	// SeverityCritical is something already broken, and is never suppressed.
	SeverityCritical Severity = "critical"
)

// ParseSeverity reads a severity name, defaulting to warning for the empty
// string so a caller that says nothing still gets a notification. Guessing
// silence would be the wrong default: an unheard alert is worse than a
// needless buzz.
func ParseSeverity(name string) (Severity, error) {
	switch Severity(name) {
	case SeverityInfo:
		return SeverityInfo, nil
	case SeverityWarning, "":
		return SeverityWarning, nil
	case SeverityCritical:
		return SeverityCritical, nil
	}
	return "", fmt.Errorf("unknown severity %q (want info, warning or critical)", name)
}

// Alert represents a notification to be sent.
type Alert struct {
	Title    string
	Message  string
	Severity Severity
}

// Plugin is the interface all monitoring plugins must implement.
type Plugin interface {
	Name() string
	Check(ctx context.Context) ([]Alert, error)
	Describe() string
}
