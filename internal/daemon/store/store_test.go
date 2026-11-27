package store

import (
	"testing"

	"wada-desktop-go/internal/protocol"
)

func TestStoreIdentityEpochReceiptsAndTimeline(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := s.ServerID(ctx)
	if err != nil || id1 == "" {
		t.Fatalf("server id = %q, err = %v", id1, err)
	}
	id2, err := s.ServerID(ctx)
	if err != nil || id2 != id1 {
		t.Fatalf("server id changed: %q -> %q", id1, id2)
	}
	ep1, err := s.NextEpoch(ctx)
	if err != nil || ep1 != 1 {
		t.Fatalf("epoch1 = %d, err = %v", ep1, err)
	}

	if err := s.PutCreationReceipt(ctx, CreationReceipt{
		IdempotencyKey: "k1", Kind: "session", ResourceID: "s1", RequestHash: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	gotCR, ok, err := s.GetCreationReceipt(ctx, "k1")
	if err != nil || !ok || gotCR.ResourceID != "s1" {
		t.Fatalf("creation receipt = %+v ok=%v err=%v", gotCR, ok, err)
	}

	if err := s.PutDeliveryReceipt(ctx, DeliveryReceipt{
		SessionID: "s1", MessageID: "m1", RequestHash: "rh1", Status: DeliveryPending,
	}); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 5; i++ {
		if err := s.AppendEvent(ctx, protocol.Event{
			Epoch: ep1, Seq: i, Session: "s1", Kind: protocol.EventTextDelta,
			Text: &protocol.TextEvent{Message: "a1", Delta: "x"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	// Reopen simulates daemon restart: epoch increments and pending deliveries become unknown.
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ep2, err := s.NextEpoch(ctx)
	if err != nil || ep2 != 2 {
		t.Fatalf("epoch2 = %d, err = %v", ep2, err)
	}
	if err := s.MarkPendingDeliveriesUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	dr, ok, err := s.GetDeliveryReceipt(ctx, "s1", "m1")
	if err != nil || !ok || dr.Status != DeliveryUnknown {
		t.Fatalf("delivery receipt = %+v ok=%v err=%v", dr, ok, err)
	}

	page1, _, hasMore, err := s.FetchTimeline(ctx, "s1", ep1, 1, 2)
	if err != nil || len(page1) != 2 || !hasMore || page1[0].Seq != 2 || page1[1].Seq != 3 {
		t.Fatalf("page1 = %+v hasMore=%v err=%v", page1, hasMore, err)
	}
	page2, _, hasMore, err := s.FetchTimeline(ctx, "s1", ep1, 3, 5)
	if err != nil || len(page2) != 2 || hasMore || page2[0].Seq != 4 || page2[1].Seq != 5 {
		t.Fatalf("page2 = %+v hasMore=%v err=%v", page2, hasMore, err)
	}
}
