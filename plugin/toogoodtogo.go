package plugin

import (
	"context"
	"fmt"
	"log/slog"
)

// TooGoodToGo monitors TooGoodToGo for available surprise bags.
// NOTE: This is a skeleton — the TGTG API requires a complex auth flow
// (email-based OTP) that needs to be implemented separately.
type TooGoodToGo struct {
	email    string
	storeIDs []string
	// seen tracks store IDs that have already been alerted to avoid duplicates.
	seen map[string]bool
}

func NewTooGoodToGo(email string, storeIDs []string) *TooGoodToGo {
	return &TooGoodToGo{
		email:    email,
		storeIDs: storeIDs,
		seen:     make(map[string]bool),
	}
}

func (t *TooGoodToGo) Name() string { return "toogoodtogo" }

func (t *TooGoodToGo) Describe() string {
	return fmt.Sprintf("  Email: %s\n  Stores: %d\n", t.email, len(t.storeIDs))
}

func (t *TooGoodToGo) Check(ctx context.Context) ([]Alert, error) {
	// TODO: Implement TGTG API auth and polling.
	// The flow is:
	// 1. POST /auth/v3/authByEmail — sends OTP to email
	// 2. POST /auth/v3/authByRequestPollingId — poll for token
	// 3. POST /item/v8/ — list available bags for store_ids
	// 4. Compare available_count > 0, dedup with t.seen

	slog.Debug("toogoodtogo check skipped: not yet implemented",
		"email", t.email,
		"stores", fmt.Sprintf("%v", t.storeIDs))

	return nil, nil
}
