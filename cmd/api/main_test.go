package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":                    "postgres://u:p@h/db",
		"KAFKA_BROKERS":                   " a:9092, b:9092 ,",
		"DELIVERY_PROMISE_CONSUMER_GROUP": "g",
	}
}

func TestLoadRuntimeAcceptsAValidEnvironment(t *testing.T) {
	rt, err := loadRuntime(envOf(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.brokers) != 2 || rt.brokers[0] != "a:9092" || rt.brokers[1] != "b:9092" {
		t.Fatalf("brokers = %v", rt.brokers)
	}
	if rt.group != "g" || rt.httpAddr != ":8080" || rt.migrationsURL != rt.databaseURL {
		t.Fatalf("rt = %+v", rt)
	}
}

func TestLoadRuntimeNamesTheMissingVariable(t *testing.T) {
	cases := []struct{ drop, want string }{
		{"DATABASE_URL", "DATABASE_URL"},
		{"KAFKA_BROKERS", "KAFKA_BROKERS"},
		{"DELIVERY_PROMISE_CONSUMER_GROUP", "DELIVERY_PROMISE_CONSUMER_GROUP"},
	}
	for _, tc := range cases {
		t.Run(tc.drop, func(t *testing.T) {
			env := validEnv()
			delete(env, tc.drop)
			_, err := loadRuntime(envOf(env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoadRuntimeRefusesTheLogPublisherAndKeepsOverrides(t *testing.T) {
	env := validEnv()
	env["EVENT_PUBLISHER"] = "log"
	if _, err := loadRuntime(envOf(env)); err == nil || !strings.Contains(err.Error(), "EVENT_PUBLISHER") {
		t.Fatalf("err = %v", err)
	}
	env["EVENT_PUBLISHER"] = "kafka"
	env["MIGRATIONS_DATABASE_URL"] = "postgres://direct"
	env["HTTP_ADDR"] = ":9999"
	rt, err := loadRuntime(envOf(env))
	if err != nil || rt.migrationsURL != "postgres://direct" || rt.httpAddr != ":9999" {
		t.Fatalf("rt=%+v err=%v", rt, err)
	}
}

func TestParseDuration(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", time.Second}, {"250ms", 250 * time.Millisecond}, {"2", 2 * time.Second},
		{"0", time.Second}, {"-3s", time.Second}, {"nonsense", time.Second},
	}
	for _, tc := range cases {
		if got := parseDuration("X", tc.raw, time.Second, log); got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestOutboxMaxAttempts(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	cases := map[string]int{"": 20, "5": 5, "0": 0, "-1": 20, "x": 20}
	for raw, want := range cases {
		if got := outboxMaxAttempts(raw, log); got != want {
			t.Errorf("outboxMaxAttempts(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestShutdownDrainDelay(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	cases := map[string]time.Duration{"": 5 * time.Second, "0": 0, "2s": 2 * time.Second, "-1s": 5 * time.Second, "x": 5 * time.Second}
	for raw, want := range cases {
		if got := shutdownDrainDelay(raw, log); got != want {
			t.Errorf("shutdownDrainDelay(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for _, lvl := range []string{"debug", "warn", "WARNING", "error", "info", "bogus"} {
		if newLogger(lvl) == nil {
			t.Fatalf("no logger for %q", lvl)
		}
	}
}

func TestJoinBrokers(t *testing.T) {
	if got := joinBrokers([]string{"a:1", "b:2"}); got != "a:1,b:2" {
		t.Fatalf("got %q", got)
	}
	if got := joinBrokers(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

type fakeSweeper struct {
	calls []string
	fail  string
}

func (f *fakeSweeper) pass(name string) (int, error) {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return 0, errors.New("boom")
	}
	return 1, nil
}
func (f *fakeSweeper) ExpireOffers(context.Context, int) (int, error) { return f.pass("expire") }
func (f *fakeSweeper) ReleaseExpiredHolds(context.Context, int) (int, error) {
	return f.pass("release")
}
func (f *fakeSweeper) CloseBooks(context.Context, int) (int, error)    { return f.pass("close") }
func (f *fakeSweeper) RepriceAtRisk(context.Context, int) (int, error) { return f.pass("reprice") }

func TestSweepOnceRunsEveryPassEvenWhenOneFails(t *testing.T) {
	var buf bytes.Buffer
	f := &fakeSweeper{fail: "release"}
	sweepOnce(context.Background(), f, slog.New(slog.NewTextHandler(&buf, nil)))
	if strings.Join(f.calls, ",") != "expire,release,close,reprice" {
		t.Fatalf("calls = %v", f.calls)
	}
	if !strings.Contains(buf.String(), "sweep pass failed") || !strings.Contains(buf.String(), "release expired holds") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestRunSweeperTicksUntilCancelled(t *testing.T) {
	f := &fakeSweeper{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSweeper(ctx, f, time.Millisecond, slog.New(slog.DiscardHandler)) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) < 4 {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestStartWorkerStopsOnCancelAndAwaitBounds(t *testing.T) {
	w := startWorker("w", slog.New(slog.DiscardHandler), func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	select {
	case <-w.done:
		t.Fatal("worker must run until stopped")
	case <-time.After(10 * time.Millisecond):
	}
	died := watchWorkers(w)
	w.stop()
	if name := <-died; name != "w" {
		t.Fatalf("died = %q", name)
	}
	await(slog.New(slog.DiscardHandler), w)
}

func TestStartWorkerLogsAFailure(t *testing.T) {
	var buf bytes.Buffer
	w := startWorker("bad", slog.New(slog.NewTextHandler(&buf, nil)), func(context.Context) error { return errors.New("kaput") })
	<-w.done
	if !strings.Contains(buf.String(), "kaput") {
		t.Fatalf("log = %s", buf.String())
	}
}
