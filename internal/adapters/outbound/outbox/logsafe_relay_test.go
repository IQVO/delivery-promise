package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// crlfFailingStore fails every drain on a row whose event type, key and send
// error carry CR/LF, already at the attempt cap.
type crlfFailingStore struct{ err error }

func (s crlfFailingStore) Drain(context.Context, int, int, func(context.Context, Message) error) (int, error) {
	return 0, s.err
}

// TestRelayLogsNoEventDerivedLineBreaks: the attempt-cap and pass-failed logs
// of the relay must carry no line break from the outbox row or the send error.
func TestRelayLogsNoEventDerivedLineBreaks(t *testing.T) {
	rec := newCRLFRecorder()
	store := crlfFailingStore{err: &SendFailure{
		RowID: 1, Attempts: 3, Err: errors.New("broker refused" + crlfForged),
		Message: Message{EventID: "id" + crlfForged, Topic: "logsafe.topic", EventType: "logsafe.Evil" + crlfForged, Key: []byte("key" + crlfForged)},
	}}
	r := NewOutboxRelay(store, nil, slog.New(rec), WithMaxAttempts(3), WithInterval(time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)
	rec.assertNoLineBreaks(t, 2)
}
