package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_ProjectUserMappings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.json")
	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	projectID := int64(1)
	telegramID := int64(100)
	taigaUserID := int64(200)

	if err := st.SetProjectUserMapping(projectID, telegramID, taigaUserID); err != nil {
		t.Fatalf("SetProjectUserMapping: %v", err)
	}

	got, ok := st.GetProjectUserMapping(projectID, telegramID)
	if !ok {
		t.Fatalf("expected mapping")
	}
	if got != taigaUserID {
		t.Fatalf("unexpected mapping: got=%d want=%d", got, taigaUserID)
	}

	m := st.ListProjectUserMappings(projectID)
	if len(m) != 1 {
		t.Fatalf("unexpected mappings len: %d", len(m))
	}
	if m[telegramID] != taigaUserID {
		t.Fatalf("unexpected mapping value: got=%d want=%d", m[telegramID], taigaUserID)
	}

	if err := st.RemoveProjectUserMapping(projectID, telegramID); err != nil {
		t.Fatalf("RemoveProjectUserMapping: %v", err)
	}
	_, ok = st.GetProjectUserMapping(projectID, telegramID)
	if ok {
		t.Fatalf("expected mapping to be removed")
	}
}

func TestStore_LoadLegacyFormat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.json")
	legacy := map[int64]UserLink{
		123: {
			TelegramID:     123,
			TaigaToken:     "t",
			TaigaUserID:    456,
			TaigaUserName:  "name",
			LastTaskStates: map[int64]TaskDigest{},
		},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal legacy: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	link, ok := st.Get(123)
	if !ok {
		t.Fatalf("expected link")
	}
	if link.TaigaUserID != 456 {
		t.Fatalf("unexpected taiga user id: %d", link.TaigaUserID)
	}
}

func TestStore_TelegramUsernameIndex(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.json")
	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := st.UpsertTelegramUsername("", 1); err != nil {
		t.Fatalf("UpsertTelegramUsername: %v", err)
	}
	if err := st.UpsertTelegramUsername("User", 0); err != nil {
		t.Fatalf("UpsertTelegramUsername: %v", err)
	}

	if err := st.UpsertTelegramUsername("TestUser", 123); err != nil {
		t.Fatalf("UpsertTelegramUsername: %v", err)
	}
	if err := st.UpsertTelegramUsername("@TestUser", 123); err != nil {
		t.Fatalf("UpsertTelegramUsername: %v", err)
	}

	got, ok := st.ResolveTelegramHandle("@testuser")
	if !ok {
		t.Fatalf("expected resolve")
	}
	if got != 123 {
		t.Fatalf("unexpected id: got=%d want=%d", got, 123)
	}

	st2, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, ok = st2.ResolveTelegramHandle("testuser")
	if !ok {
		t.Fatalf("expected resolve after reload")
	}
	if got != 123 {
		t.Fatalf("unexpected id after reload: got=%d want=%d", got, 123)
	}
}
