//go:build integration

package main

import (
	"os"
	"testing"

	"github.com/claudioed/delivery-promise/internal/testing/kafkatest"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
