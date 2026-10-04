package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/solessfir/lazyp4/internal/p4"
)

func TestProgressOffersOnlyAvailableCancellation(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.width, a.opRunning, a.opName = 100, true, "Shelving"
	if strings.Contains(a.renderProgressBar(), "cancel") || strings.Contains(a.helpContent(), "Cancel operation") {
		t.Fatal("uncancellable operation offers cancellation")
	}
	_, a.opCancel = context.WithCancel(context.Background())
	defer a.opCancel()
	if !strings.Contains(a.renderProgressBar(), "cancel") || !strings.Contains(a.helpContent(), "Cancel operation") {
		t.Fatal("cancellable operation does not offer cancellation")
	}
}
