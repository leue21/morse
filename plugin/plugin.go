package plugin

import (
	"context"
	"errors"
)

// ErrNeedsLogin is returned by Check when the plugin requires interactive login.
var ErrNeedsLogin = errors.New("login required")

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

// LoginStarter is an optional interface for plugins that require interactive login.
type LoginStarter interface {
	NeedsLogin() bool
	StartLogin(ctx context.Context) error
	SubmitPIN(ctx context.Context, pin string) error
}
