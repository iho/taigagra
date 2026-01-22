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

package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// UserLink stores the Taiga credentials tied to a Telegram user.
type UserLink struct {
	NotifyChatID    *int64               `json:"notify_chat_id,omitempty"`
	LastTaskStates  map[int64]TaskDigest `json:"last_task_states"`
	TaigaToken      string               `json:"taiga_token"`
	TaigaUserName   string               `json:"taiga_user_name"`
	WatchedProjects []int64              `json:"watched_projects,omitempty"`
	TelegramID      int64                `json:"telegram_id"`
	TaigaUserID     int64                `json:"taiga_user_id"`
}

// TaskDigest captures key fields to detect changes between polling cycles.
type TaskDigest struct {
	Status     string `json:"status"`
	AssignedTo int64  `json:"assigned_to"`
}

// Store persists user links.
type Store struct {
	links               map[int64]UserLink
	projectUserMappings map[int64]map[int64]int64
	telegramUsernames   map[string]int64
	path                string
	mu                  sync.Mutex
}

type diskData struct {
	Links               map[int64]UserLink        `json:"links"`
	ProjectUserMappings map[int64]map[int64]int64 `json:"project_user_mappings,omitempty"`
	TelegramUsernames   map[string]int64          `json:"telegram_usernames,omitempty"`
}

// New creates or loads a store from disk.
func New(path string) (*Store, error) {
	store := &Store{
		path:                path,
		links:               make(map[int64]UserLink),
		projectUserMappings: make(map[int64]map[int64]int64),
		telegramUsernames:   make(map[string]int64),
	}
	err := store.load()
	if err != nil {
		return nil, err
	}

	return store, nil
}

// Get returns the link for a telegram user.
func (s *Store) Get(telegramID int64) (UserLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[telegramID]

	return link, ok
}

// Save inserts or updates a link.
func (s *Store) Save(link UserLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if link.LastTaskStates == nil {
		link.LastTaskStates = make(map[int64]TaskDigest)
	}

	s.links[link.TelegramID] = link

	return s.persist()
}

// Delete removes a link.
func (s *Store) Delete(telegramID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.links, telegramID)

	return s.persist()
}

// UpdateTaskState replaces the stored digest map for a user.
func (s *Store) UpdateTaskState(telegramID int64, digests map[int64]TaskDigest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}

	link.LastTaskStates = digests
	s.links[telegramID] = link

	return s.persist()
}

func (s *Store) SetNotifyChat(telegramID int64, chatID *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}

	link.NotifyChatID = chatID
	s.links[telegramID] = link

	return s.persist()
}

func (s *Store) SetProjectUserMapping(projectID, telegramID, taigaUserID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if projectID <= 0 {
		return errors.New("некоректний id проєкту")
	}

	if telegramID == 0 {
		return errors.New("некоректний id користувача Telegram")
	}

	if taigaUserID <= 0 {
		return errors.New("некоректний id користувача Taiga")
	}

	if s.projectUserMappings == nil {
		s.projectUserMappings = make(map[int64]map[int64]int64)
	}

	if s.projectUserMappings[projectID] == nil {
		s.projectUserMappings[projectID] = make(map[int64]int64)
	}

	s.projectUserMappings[projectID][telegramID] = taigaUserID

	return s.persist()
}

func (s *Store) RemoveProjectUserMapping(projectID, telegramID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if projectID <= 0 {
		return errors.New("некоректний id проєкту")
	}

	if telegramID == 0 {
		return errors.New("некоректний id користувача Telegram")
	}

	if s.projectUserMappings == nil {
		return nil
	}

	if s.projectUserMappings[projectID] == nil {
		return nil
	}

	delete(s.projectUserMappings[projectID], telegramID)

	if len(s.projectUserMappings[projectID]) == 0 {
		delete(s.projectUserMappings, projectID)
	}

	return s.persist()
}

func (s *Store) GetProjectUserMapping(projectID, telegramID int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.projectUserMappings == nil {
		return 0, false
	}

	m, ok := s.projectUserMappings[projectID]
	if !ok {
		return 0, false
	}

	taigaUserID, ok := m[telegramID]

	return taigaUserID, ok
}

func (s *Store) ListProjectUserMappings(projectID int64) map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[int64]int64)
	if s.projectUserMappings == nil {
		return result
	}

	m, ok := s.projectUserMappings[projectID]
	if !ok {
		return result
	}

	for k, v := range m {
		result[k] = v
	}

	return result
}

func (s *Store) UpsertTelegramUsername(username string, telegramID int64) error {
	username = strings.TrimSpace(username)
	if username == "" || telegramID == 0 {
		return nil
	}

	username = strings.TrimPrefix(username, "@")
	username = strings.ToLower(username)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.telegramUsernames == nil {
		s.telegramUsernames = make(map[string]int64)
	}

	if existing, ok := s.telegramUsernames[username]; ok && existing == telegramID {
		return nil
	}

	s.telegramUsernames[username] = telegramID

	return s.persist()
}

func (s *Store) ResolveTelegramHandle(handle string) (int64, bool) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return 0, false
	}

	handle = strings.TrimPrefix(handle, "@")
	handle = strings.ToLower(handle)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.telegramUsernames == nil {
		return 0, false
	}

	id, ok := s.telegramUsernames[handle]

	return id, ok
}

// AddWatchedProject subscribes a telegram user to a Taiga project.
func (s *Store) AddWatchedProject(telegramID, projectID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[telegramID]
	if !ok {
		return fmt.Errorf("користувач %d не привʼязаний", telegramID)
	}

	for _, existing := range link.WatchedProjects {
		if existing == projectID {
			s.links[telegramID] = link
			return s.persist()
		}
	}

	link.WatchedProjects = append(link.WatchedProjects, projectID)
	s.links[telegramID] = link

	return s.persist()
}

// RemoveWatchedProject unsubscribes a telegram user from a Taiga project.
func (s *Store) RemoveWatchedProject(telegramID, projectID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[telegramID]
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
	s.links[telegramID] = link

	return s.persist()
}

// List returns all stored links.
func (s *Store) List() []UserLink {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]UserLink, 0, len(s.links))
	for _, link := range s.links {
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

	raw, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("не вдалося прочитати сховище: %w", err)
	}

	var dd diskData
	if err := json.Unmarshal(raw, &dd); err == nil && dd.Links != nil {
		s.links = dd.Links
		if dd.ProjectUserMappings != nil {
			s.projectUserMappings = dd.ProjectUserMappings
		}

		if dd.TelegramUsernames != nil {
			s.telegramUsernames = dd.TelegramUsernames
		}

		return nil
	}

	var legacy map[int64]UserLink
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("не вдалося прочитати сховище: %w", err)
	}

	s.links = legacy

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

	data := diskData{Links: s.links}
	if len(s.projectUserMappings) > 0 {
		data.ProjectUserMappings = s.projectUserMappings
	}

	if len(s.telegramUsernames) > 0 {
		data.TelegramUsernames = s.telegramUsernames
	}

	if err := encoder.Encode(data); err != nil {
		file.Close()
		return fmt.Errorf("не вдалося записати сховище: %w", err)
	}

	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tmpFile, s.path)
}
