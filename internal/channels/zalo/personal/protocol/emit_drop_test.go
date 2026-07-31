package protocol

import (
	"context"
	"testing"
)

// emit evicts the OLDEST entry when the inbound buffer is full. That is the
// right trade for a chat bot (better to keep the newest messages than stall the
// WebSocket reader), but it used to happen in complete silence — a customer's
// question could vanish between the socket and the agent with no log, no
// counter, and no way to know afterwards.
//
// A burst of short fragments is the normal Vietnamese Zalo typing pattern, so
// this path is reachable in ordinary use.
func TestEmitCountsDroppedMessages(t *testing.T) {
	before := DroppedMessageCount()

	ch := make(chan int, 2)
	ctx := context.Background()

	// Fill the buffer exactly — no eviction yet.
	emit(ctx, ch, 1)
	emit(ctx, ch, 2)
	if got := DroppedMessageCount() - before; got != 0 {
		t.Fatalf("dropped %d while merely filling the buffer, want 0", got)
	}

	// Overflow — this must evict and be counted.
	emit(ctx, ch, 3)
	if got := DroppedMessageCount() - before; got != 1 {
		t.Errorf("dropped count delta = %d, want 1", got)
	}

	// The NEWEST message must survive; the oldest is the one sacrificed.
	got := []int{<-ch, <-ch}
	if got[1] != 3 {
		t.Errorf("newest message lost: buffer holds %v, want the last element to be 3", got)
	}
	if got[0] == 1 {
		t.Errorf("oldest message was NOT evicted: buffer holds %v", got)
	}
}

// A cancelled context must not count a drop — nothing was lost, the caller went
// away. Counting it would make the metric useless as a data-loss signal.
func TestEmitCancelledContextIsNotADrop(t *testing.T) {
	before := DroppedMessageCount()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan int) // unbuffered: would block if not for the cancelled ctx
	emit(ctx, ch, 1)

	if got := DroppedMessageCount() - before; got != 0 {
		t.Errorf("cancelled context counted %d drops, want 0", got)
	}
}

func TestEmitNormalSendIsNotADrop(t *testing.T) {
	before := DroppedMessageCount()

	ch := make(chan int, 4)
	for i := range 4 {
		emit(context.Background(), ch, i)
	}
	if got := DroppedMessageCount() - before; got != 0 {
		t.Errorf("sends within capacity counted %d drops, want 0", got)
	}
}
