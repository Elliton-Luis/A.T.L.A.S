package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TelegramBotToken    string
	TelegramSecretToken string
	TelegramChatID      int64
	NvidiaAPIKey        string
	NvidiaModel         string
	GroqAPIKey          string
	DBPath              string
	Timezone            string
	Location            *time.Location
	HTTPPort            string
	BackupHour          string
	CalendarToken       string
	PublicURL           string
}

// Load reads .env (if present) and populates the Config from environment variables.
func Load(envPath string) (*Config, error) {
	if envPath == "" {
		envPath = ".env"
	}
	_ = loadEnvFile(envPath)

	tzName := getEnv("TIMEZONE", "America/Sao_Paulo")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.Local
	}

	var chatID int64
	if chatIDStr := getEnv("TELEGRAM_CHAT_ID", ""); chatIDStr != "" {
		if val, err := strconv.ParseInt(chatIDStr, 10, 64); err == nil {
			chatID = val
		}
	}

	cfg := &Config{
		TelegramBotToken:    getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramSecretToken: getEnv("TELEGRAM_SECRET_TOKEN", ""),
		TelegramChatID:      chatID,
		NvidiaAPIKey:        getEnv("NVIDIA_API_KEY", ""),
		NvidiaModel:         getEnv("NVIDIA_MODEL", "meta/llama-3.2-90b-vision-instruct"),
		GroqAPIKey:          getEnv("GROQ_API_KEY", ""),
		DBPath:              getEnv("DB_PATH", "data/atlas.sqlite"),
		Timezone:            tzName,
		Location:            loc,
		HTTPPort:            getEnv("HTTP_PORT", "8080"),
		BackupHour:          getEnv("BACKUP_HOUR", "03:00"),
		CalendarToken:       getEnv("CALENDAR_TOKEN", "atlas_cal_secret_token"),
		PublicURL:           strings.TrimSuffix(getEnv("PUBLIC_URL", "http://localhost:8080"), "/"),
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			if _, exists := os.LookupEnv(k); !exists {
				_ = os.Setenv(k, v)
			}
		}
	}
	return scanner.Err()
}
