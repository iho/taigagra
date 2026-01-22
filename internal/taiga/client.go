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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client provides minimal Taiga API interactions required by the bot.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	authToken  string
}

// CreateUserStory creates a new user story in Taiga.
func (c *Client) CreateUserStory(ctx context.Context, req UserStoryCreateRequest) (UserStory, error) {
	var us UserStory
	if req.ProjectID == 0 || req.Subject == "" {
		return us, errors.New("потрібні проєкт і тема")
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "userstories"})
	err := c.do(ctx, http.MethodPost, endpoint.String(), req, &us)
	if err != nil {
		return us, err
	}

	return us, nil
}

// NewClient returns a configured Taiga API client.
func NewClient(baseURL, authToken string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("некоректний базовий URL Taiga: %w", err)
	}

	if parsed.Path == "" {
		parsed.Path = "/"
	}

	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}

	return &Client{
		baseURL:    parsed,
		authToken:  authToken,
		httpClient: &http.Client{},
	}, nil
}

// TaskCreateRequest represents payload accepted by Taiga for task creation.
type TaskCreateRequest struct {
	StatusID    *int64   `json:"status,omitempty"`
	Assigned    *int64   `json:"assigned_to,omitempty"`
	UserStory   *int64   `json:"user_story,omitempty"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ProjectID   int64    `json:"project"`
}

type StatusExtraInfo struct {
	Name string `json:"name"`
}

// UserStoryCreateRequest represents payload accepted by Taiga for user story creation.
type UserStoryCreateRequest struct {
	StatusID    *int64   `json:"status,omitempty"`
	Assigned    *int64   `json:"assigned_to,omitempty"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ProjectID   int64    `json:"project"`
}

// UserStory represents a Taiga user story subset used by the bot.
type UserStory struct {
	AssignedTo      *int64          `json:"assigned_to"`
	Subject         string          `json:"subject"`
	StatusExtraInfo StatusExtraInfo `json:"status_extra_info"`
	ID              int64           `json:"id"`
	Ref             int64           `json:"ref"`
}

// Task represents a Taiga task subset used by the bot.
type Task struct {
	AssignedTo      *int64          `json:"assigned_to"`
	Subject         string          `json:"subject"`
	StatusExtraInfo StatusExtraInfo `json:"status_extra_info"`
	ID              int64           `json:"id"`
	Ref             int64           `json:"ref"`
}

// User represents Taiga user minimal fields.
type User struct {
	FullName string `json:"full_name_display"`
	ID       int64  `json:"id"`
}

// Project represents a Taiga project subset used by the bot.
type Project struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	ID   int64  `json:"id"`
}

type Membership struct {
	FullName string `json:"full_name"`
	ID       int64  `json:"id"`
	Project  int64  `json:"project"`
	UserID   int64  `json:"user"`
	IsAdmin  bool   `json:"is_admin"`
	IsOwner  bool   `json:"is_owner"`
}

// CreateTask creates a new task in Taiga.
func (c *Client) CreateTask(ctx context.Context, req TaskCreateRequest) (Task, error) {
	var task Task
	if req.ProjectID == 0 || req.Subject == "" {
		return task, errors.New("потрібні проєкт і тема")
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "tasks"})
	err := c.do(ctx, http.MethodPost, endpoint.String(), req, &task)
	if err != nil {
		return task, err
	}

	return task, nil
}

// GetUser fetches user by id.
func (c *Client) GetUser(ctx context.Context, id int64) (User, error) {
	var user User

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: fmt.Sprintf("users/%d", id)})
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &user)
	if err != nil {
		return user, err
	}

	return user, nil
}

// GetMe fetches the authenticated user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var user User

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "users/me"})
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &user)
	if err != nil {
		return user, err
	}

	return user, nil
}

// ListTasksParams defines filters for ListTasks.
type ListTasksParams struct {
	AssignedTo *int64
	StatusID   *int64
	ProjectID  int64
}

// ListTasks fetches tasks using optional filters.
func (c *Client) ListTasks(ctx context.Context, params ListTasksParams) ([]Task, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "tasks"})

	query := endpoint.Query()
	if params.ProjectID != 0 {
		query.Set("project", strconv.FormatInt(params.ProjectID, 10))
	}

	if params.AssignedTo != nil {
		query.Set("assigned_to", strconv.FormatInt(*params.AssignedTo, 10))
	}

	if params.StatusID != nil {
		query.Set("status", strconv.FormatInt(*params.StatusID, 10))
	}

	endpoint.RawQuery = query.Encode()

	var tasks []Task
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &tasks)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// ListUserStoriesParams defines filters for ListUserStories.
type ListUserStoriesParams struct {
	AssignedTo *int64
	StatusID   *int64
	ProjectID  int64
}

// ListUserStories fetches user stories using optional filters.
func (c *Client) ListUserStories(ctx context.Context, params ListUserStoriesParams) ([]UserStory, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "userstories"})

	query := endpoint.Query()
	if params.ProjectID != 0 {
		query.Set("project", strconv.FormatInt(params.ProjectID, 10))
	}

	if params.AssignedTo != nil {
		query.Set("assigned_to", strconv.FormatInt(*params.AssignedTo, 10))
	}

	if params.StatusID != nil {
		query.Set("status", strconv.FormatInt(*params.StatusID, 10))
	}

	endpoint.RawQuery = query.Encode()

	var stories []UserStory
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &stories)
	if err != nil {
		return nil, err
	}

	return stories, nil
}

// ListProjects fetches projects available for current user.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "projects"})

	var projects []Project
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &projects)
	if err != nil {
		return nil, err
	}

	return projects, nil
}

func (c *Client) ListMemberships(ctx context.Context, projectID int64) ([]Membership, error) {
	if projectID <= 0 {
		return nil, errors.New("некоректний id проєкту")
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "memberships"})
	query := endpoint.Query()
	query.Set("project", strconv.FormatInt(projectID, 10))

	endpoint.RawQuery = query.Encode()

	var memberships []Membership
	err := c.do(ctx, http.MethodGet, endpoint.String(), nil, &memberships)
	if err != nil {
		return nil, err
	}

	return memberships, nil
}

// do executes HTTP request and decodes the response.
func (c *Client) do(ctx context.Context, method, endpoint string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("не вдалося серіалізувати запит: %w", err)
		}

		body = bytes.NewBuffer(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("не вдалося сформувати запит: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("не вдалося виконати запит: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))

	finalURL := endpoint
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("помилка API Taiga (%d) з %s: %s", resp.StatusCode, finalURL, truncateForLog(string(bodyBytes), 1024))
	}

	if out == nil {
		return nil
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "json") {
		return fmt.Errorf("API Taiga повернув не-JSON content-type %q з %s: %s", contentType, finalURL, truncateForLog(string(bodyBytes), 1024))
	}

	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(out); err != nil {
		return fmt.Errorf("не вдалося розібрати відповідь з %s (content-type %q): %w", finalURL, contentType, err)
	}

	return nil
}

func truncateForLog(body string, max int) string {
	body = strings.TrimSpace(body)

	if max <= 0 {
		return ""
	}

	if len(body) <= max {
		return body
	}

	return body[:max] + "..."
}
