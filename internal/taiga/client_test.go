//
// Copyright (c) 2026 Sumicare
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package taiga

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

		_, err = c.ListMemberships(t.Context(), 0)
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

		got, err := c.ListMemberships(t.Context(), 1)
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

func TestClient_AutoRefreshOnUnauthorized(t *testing.T) {
	t.Parallel()

	const (
		oldAuth    = "old-auth"
		oldRefresh = "old-refresh"
		newAuth    = "new-auth"
		newRefresh = "new-refresh"
		projectID  = int64(1)
	)

	var membershipsCalls int
	var refreshCalls int
	var gotAuthAfterRefresh string
	var gotRefreshAfterRefresh string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/memberships":
			membershipsCalls++
			if membershipsCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+newAuth {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("unexpected auth header: " + got))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]Membership{{ID: 1, Project: projectID, UserID: 5, FullName: "Admin"}})
		case "/api/v1/auth/refresh":
			refreshCalls++
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("unexpected method"))
				return
			}
			var req struct {
				Refresh string `json:"refresh"`
			}
			body, _ := io.ReadAll(r.Body)
			if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("invalid json"))
				return
			}
			if req.Refresh != oldRefresh {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("unexpected refresh"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"auth_token": newAuth, "refresh": newRefresh})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := NewClientWithTokens(srv.URL+"/api/v1", oldAuth, oldRefresh, func(authToken, refreshToken string) {
		gotAuthAfterRefresh = authToken
		gotRefreshAfterRefresh = refreshToken
	})
	if err != nil {
		t.Fatalf("NewClientWithTokens: %v", err)
	}

	got, err := c.ListMemberships(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("unexpected len: %d", len(got))
	}
	if membershipsCalls != 2 {
		t.Fatalf("unexpected memberships calls: %d", membershipsCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("unexpected refresh calls: %d", refreshCalls)
	}
	if gotAuthAfterRefresh != newAuth {
		t.Fatalf("unexpected callback auth token")
	}
	if gotRefreshAfterRefresh != newRefresh {
		t.Fatalf("unexpected callback refresh token")
	}
}
