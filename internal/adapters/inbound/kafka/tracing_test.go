package kafka

import (
	"context"
	"errors"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

const (
	publisherTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	probeType            = "com.retailnetwork.commerce.test.probe.Probed"
)

// tracedHandle feeds one message with the given headers through handleTraced
// and returns the span context the handler saw and the consumer's ended spans.
// The handler fails with failWith when it is non-nil.
func tracedHandle(t *testing.T, headers []kafkago.Header, failWith error) (trace.SpanContext, []sdktrace.ReadOnlySpan) {
	t.Helper()
	tp, prop := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTracerProvider(tp); otel.SetTextMapPropagator(prop) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var seen trace.SpanContext
	c := &Consumer{Name: "probe", Logger: discard(), Handlers: map[string]HandlerFunc{
		probeType: func(ctx context.Context, _ usecases.Meta, _ ce.Event) error {
			seen = trace.SpanContextFromContext(ctx)
			return failWith
		},
	}}
	m := kafkago.Message{Topic: "t", Headers: headers, Value: message(t, "evt-1", probeType, map[string]any{})}
	if err := c.handleTraced(context.Background(), m); !errors.Is(err, failWith) {
		t.Fatalf("handleTraced = %v, want %v", err, failWith)
	}
	return seen, recorder.Ended()
}

// The consumer span is a consumer-kind span named "<topic> process" whose parent
// is the publisher's span carried in traceparent, and the handler runs inside it.
func TestConsumerSpanIsParentedToThePublisherTrace(t *testing.T) {
	seen, spans := tracedHandle(t, []kafkago.Header{
		{Key: "content-type", Value: []byte("application/cloudevents+json; charset=UTF-8")},
		{Key: "traceparent", Value: []byte(publisherTraceparent)},
	}, nil)

	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	span := spans[0]
	if span.SpanKind() != trace.SpanKindConsumer || span.Name() != "t process" {
		t.Errorf("span = %v %q, want consumer \"t process\"", span.SpanKind(), span.Name())
	}
	if got := span.SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %s, want the publisher's", got)
	}
	if got := span.Parent().SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("parent span id = %s, want the publish span's", got)
	}
	if seen.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("handler ran under span %s, want the consumer span %s", seen.SpanID(), span.SpanContext().SpanID())
	}
}

func TestConsumerWithoutTraceHeadersStartsANewTrace(t *testing.T) {
	_, spans := tracedHandle(t, nil, nil)
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].Parent().IsValid() {
		t.Errorf("parent = %v, want none for a message without traceparent", spans[0].Parent())
	}
}

func TestConsumerSpanRecordsAHandlerFailure(t *testing.T) {
	_, spans := tracedHandle(t, []kafkago.Header{{Key: "traceparent", Value: []byte(publisherTraceparent)}}, errors.New("boom"))
	if len(spans) != 1 || spans[0].Status().Code != codes.Error {
		t.Fatalf("spans = %v, want one errored consumer span", spans)
	}
}

func TestConsumerSpanNamesTheConsumerWhenTheMessageHasNoTopic(t *testing.T) {
	c := &Consumer{Name: "probe", Logger: discard()}
	recorder := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	if err := c.handleTraced(context.Background(), kafkago.Message{Value: message(t, "evt-1", probeType, map[string]any{})}); err != nil {
		t.Fatal(err)
	}
	if spans := recorder.Ended(); len(spans) != 1 || spans[0].Name() != "probe process" {
		t.Fatalf("spans = %v, want \"probe process\"", spans)
	}
}
