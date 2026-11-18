package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultMaxBodyBytes(t *testing.T) {
	const want = 1024 * 1024 * 1024
	if DefaultMaxBodyBytes != want {
		t.Fatalf("default body limit = %d, want %d", DefaultMaxBodyBytes, want)
	}
	_, accountRepo, _, _ := setupTestDB(t)
	handler, err := NewProxyHandler(accountRepo)
	if err != nil {
		t.Fatal(err)
	}
	if handler.cfg.MaxBodyBytes != want {
		t.Fatalf("handler body limit = %d, want %d", handler.cfg.MaxBodyBytes, want)
	}
}

func TestBufferedRequestLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int
		limit    int64
		tooLarge bool
	}{
		{"below", 7, 8, false},
		{"at", 8, 8, false},
		{"above", 9, 8, true},
		{"zero uses default", 9, 0, false},
		{"negative uses default", 9, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", tc.size)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			buffered, err := NewBufferedRequestWithLimit(req, tc.limit)
			if tc.tooLarge {
				if !errors.Is(err, ErrRequestBodyTooLarge) {
					t.Fatalf("error = %v, want ErrRequestBodyTooLarge", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(buffered.Bytes()) != body {
				t.Fatal("buffered body changed")
			}
			replayed, err := io.ReadAll(req.Body)
			if err != nil || string(replayed) != body {
				t.Fatalf("restored body = %q, error = %v", replayed, err)
			}
		})
	}
}
