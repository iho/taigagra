package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// UserLink stores the Taiga credentials tied to a Telegram user.
type UserLink struct {
	TelegramID      int64                `json:"telegram_id"`
	TaigaToken      string               `json:"taiga_token"`
	TaigaUserID     int64                `json:"taiga_user_id"`
	TaigaUserName   string               `json:"taiga_user_name"`
	NotifyChatID    *int64               `json:"notify_chat_id,omitempty"`
	WatchedProjects []int64              `json:"watched_projects,omitempty"`
	LastTaskStates  map[int64]TaskDigest `json:"last_task_states"`
}

// TaskDigest captures key fields to detect changes between polling cycles.
type TaskDigest struct {
	Status     string `json:"status"`
	AssignedTo int64  `json:"assigned_to"`
}

// Store persists user links.
type Store struct {
	path string
	mu   sync.Mutex
	data map[int64]UserLink
}

// New creates or loads a store from disk.
func New(path string) (*Store, error) {
	store := &Store{
		path: path,
		data: make(map[int64]UserLink),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Get returns the link for a telegram user.
func (s *Store) Get(telegramID int64) (UserLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.data[telegramID]
	return link, ok
}

// Save inserts or updates a link.
func (s *Store) Save(link UserLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if link.LastTaskStates == nil {
		link.LastTaskStates = make(map[int64]TaskDigest)
	}
	s.data[link.TelegramID] = link
	return s.persist()
}

// Delete removes a link.
func (s *Store) Delete(telegramID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, telegramID)
	return s.persist()
}

// UpdateTaskState replaces the stored digest map for a user.
func (s *Store) UpdateTaskState(telegramID int64, digests map[int64]TaskDigest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.data[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}
	link.LastTaskStates = digests
	s.data[telegramID] = link
	return s.persist()
}

func (s *Store) SetNotifyChat(telegramID int64, chatID *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.data[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}
	link.NotifyChatID = chatID
	s.data[telegramID] = link
	return s.persist()
}

// AddWatchedProject subscribes a telegram user to a Taiga project.
func (s *Store) AddWatchedProject(telegramID int64, projectID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.data[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}
	for _, existing := range link.WatchedProjects {
		if existing == projectID {
			s.data[telegramID] = link
			return s.persist()
		}
	}
	link.WatchedProjects = append(link.WatchedProjects, projectID)
	s.data[telegramID] = link
	return s.persist()
}

// RemoveWatchedProject unsubscribes a telegram user from a Taiga project.
func (s *Store) RemoveWatchedProject(telegramID int64, projectID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.data[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}
	filtered := make([]int64, 0, len(link.WatchedProjects))
	for _, existing := range link.WatchedProjects {
		if existing != projectID {
			filtered = append(filtered, existing)
		}
	}
	link.WatchedProjects = filtered
	s.data[telegramID] = link
	return s.persist()
}

// List returns all stored links.
func (s *Store) List() []UserLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]UserLink, 0, len(s.data))
	for _, link := range s.data {
		result = append(result, link)
	}
	return result
}

func (s *Store) load() error {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&s.data); err != nil {
		return fmt.Errorf("не вдалося прочитати сховище: %w", err)
	}
	return nil
}

func (s *Store) persist() error {
	tmpFile := s.path + ".tmp"
	file, err := os.Create(tmpFile)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.data); err != nil {
		file.Close()
		return fmt.Errorf("не вдалося записати сховище: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpFile, s.path)
}
