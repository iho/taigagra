package taiga

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListMemberships(t *testing.T) {
	t.Parallel()

	t.Run("invalid_project_id", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient("https://example.com/api/v1", "token")
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}

		_, err = c.ListMemberships(context.Background(), 0)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		expected := []Membership{
			{ID: 1, Project: 1, UserID: 14, FullName: "Miguel Molina"},
			{ID: 2, Project: 1, UserID: 5, FullName: "Administrator"},
		}

		errCh := make(chan error, 1)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				errCh <- fmt.Errorf("unexpected method: %s", r.Method)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.URL.Path != "/api/v1/memberships" {
				errCh <- fmt.Errorf("unexpected path: %s", r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got := r.URL.Query().Get("project"); got != "1" {
				errCh <- fmt.Errorf("unexpected project query: %q", got)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token" {
				errCh <- fmt.Errorf("unexpected auth header: %q", got)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(expected)
		}))
		defer srv.Close()

		c, err := NewClient(srv.URL+"/api/v1", "token")
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}

		got, err := c.ListMemberships(context.Background(), 1)
		if err != nil {
			t.Fatalf("ListMemberships: %v", err)
		}
		select {
		case err := <-errCh:
			t.Fatalf("server assertion failed: %v", err)
		default:
		}

		if len(got) != len(expected) {
			t.Fatalf("unexpected len: got=%d want=%d", len(got), len(expected))
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("unexpected item[%d]: got=%+v want=%+v", i, got[i], expected[i])
			}
		}
	})
}
