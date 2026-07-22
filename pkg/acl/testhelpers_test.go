package acl

import (
	"io"
	"strings"
	"testing"

	"github.com/go-kit/log"
)

func nopLogger() log.Logger { return log.NewNopLogger() }

func stringReader(s string) io.Reader { return strings.NewReader(s) }

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// mustSelectors parses a query into selectors, failing the test on error.
func mustSelectors(t *testing.T, query string) []selector {
	t.Helper()
	sels, err := parseSelectors(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return sels
}
