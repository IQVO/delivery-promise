package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// TestHandleMessageLogsNoEventDerivedLineBreaks feeds the consumer a payload
// that is not a CloudEvent and a CloudEvent whose id, type, subject and data
// carry CR/LF, with a handler refusing it with an error quoting the payload:
// no log attribute may carry the line break.
func TestHandleMessageLogsNoEventDerivedLineBreaks(t *testing.T) {
	rec := newCRLFRecorder()
	evilType := "logsafe.Evil" + crlfForged
	c := &Consumer{
		Name: "logsafe", Logger: slog.New(rec),
		Handlers: map[string]HandlerFunc{
			evilType: func(context.Context, usecases.Meta, ce.Event) error {
				return fmt.Errorf("%w: field"+crlfForged, errBadPayload)
			},
		},
	}
	ctx := context.Background()
	_ = c.HandleMessage(ctx, []byte("not a CloudEvent"+crlfForged))
	_ = c.HandleMessage(ctx, crlfCloudEvent(t, "id"+crlfForged, evilType))
	rec.assertNoLineBreaks(t, 2)
}

// crlfCloudEvent is a structured-mode CloudEvent whose string attributes are
// the given, hostile values.
func crlfCloudEvent(t *testing.T, id, typ string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "type": typ, "source": "urn:logsafe",
		"subject": "subject" + crlfForged, "datacontenttype": "application/json",
		"data": map[string]any{"field": "value" + crlfForged},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
