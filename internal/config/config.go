package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds application level configuration values.
type Config struct {
	TelegramToken string
	TaigaBaseURL  string
	StoragePath   string
	PollInterval  time.Duration
}

const (
	taigaBaseURLKey  = "TAIGA_BASE_URL"
	telegramTokenKey = "TELEGRAM_BOT_TOKEN"
	storagePathKey   = "LINK_STORAGE_PATH"
	pollIntervalKey  = "POLL_INTERVAL_SECONDS"
)

// Load reads configuration from the environment applying reasonable defaults where possible.
func Load() (Config, error) {
	telegramToken := os.Getenv(telegramTokenKey)
	if telegramToken == "" {
		return Config{}, fmt.Errorf("%s is required", telegramTokenKey)
	}

	taigaBaseURL := os.Getenv(taigaBaseURLKey)
	if taigaBaseURL == "" {
		taigaBaseURL = "https://api.taiga.io/api/v1"
	}

	storagePath := os.Getenv(storagePathKey)
	if storagePath == "" {
		storagePath = "taiga_links.json"
	}

	pollInterval := 30 * time.Second
	if raw := os.Getenv(pollIntervalKey); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid %s: %w", pollIntervalKey, err)
		}
		if seconds <= 0 {
			return Config{}, fmt.Errorf("%s must be positive", pollIntervalKey)
		}
		pollInterval = time.Duration(seconds) * time.Second
	}

	return Config{
		TelegramToken: telegramToken,
		TaigaBaseURL:  taigaBaseURL,
		StoragePath:   storagePath,
		PollInterval:  pollInterval,
	}, nil
}
