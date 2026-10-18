package main

import (
	"context"
	"errors"
	"testing"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"
)

func TestPinnedSessionsBeyondFirstPage(t *testing.T) {
	calls := 0
	rows, err := runner.LoadPinnedSessions(context.Background(), []string{"new", "old"}, []opencode.Session{{ID: "new"}}, "page2",
		func(_ context.Context, cursor, search string) ([]opencode.Session, string, error) {
			calls++
			if cursor != "page2" || search != "" {
				t.Fatalf("lookup %q, %q", cursor, search)
			}
			return []opencode.Session{{ID: "other"}, {ID: "old", Title: "Saved older chat"}}, "page3", nil
		})
	if err != nil || calls != 1 || len(rows) != 1 || rows[0].ID != "old" {
		t.Fatalf("pins: %+v, %v; calls %d", rows, err, calls)
	}
}

func TestPinnedSessionLookupErrorsAndMissingPins(t *testing.T) {
	for _, scenario := range []string{"missing", "error", "repeated cursor"} {
		t.Run(scenario, func(t *testing.T) {
			_, err := runner.LoadPinnedSessions(context.Background(), []string{"gone"}, nil, "next",
				func(context.Context, string, string) ([]opencode.Session, string, error) {
					if scenario == "error" {
						return nil, "", errors.New("offline")
					}
					if scenario == "repeated cursor" {
						return nil, "next", nil
					}
					return nil, "", nil
				})
			if (err != nil) != (scenario != "missing") {
				t.Fatalf("unexpected lookup result: %v", err)
			}
		})
	}
}
