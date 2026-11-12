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
	MaxTokensQuick      int
	MaxTokensHeavy      int
	MaxTokensResearch   int
	HistoryLimit        int
	TursoDatabaseURL    string
	TursoAuthToken      string
	CronSecret          string
	SonarBridgeURL      string
	SonarBridgeToken    string
	SonarCLIPath        string
	ProjectsDir         string
	IMAPHost            string
	IMAPPort            string
	IMAPUser            string
	IMAPPass            string
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

	maxQuick, _ := strconv.Atoi(getEnv("MAX_TOKENS_QUICK", "450"))
	if maxQuick <= 0 {
		maxQuick = 450
	}
	maxHeavy, _ := strconv.Atoi(getEnv("MAX_TOKENS_HEAVY", "2048"))
	if maxHeavy <= 0 {
		maxHeavy = 2048
	}
	maxResearch, _ := strconv.Atoi(getEnv("MAX_TOKENS_RESEARCH", "4096"))
	if maxResearch <= 0 {
		maxResearch = 4096
	}
	histLimit, _ := strconv.Atoi(getEnv("CHAT_HISTORY_LIMIT", "4"))
	if histLimit <= 0 {
		histLimit = 4
	}

	cfg := &Config{
		TelegramBotToken:    getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramSecretToken: getEnv("TELEGRAM_SECRET_TOKEN", ""),
		TelegramChatID:      chatID,
		NvidiaAPIKey:        getEnv("NVIDIA_API_KEY", ""),
		NvidiaModel:         getEnv("NVIDIA_MODEL", "nvidia/nemotron-3.5-bolt-e4b"),
		GroqAPIKey:          getEnv("GROQ_API_KEY", ""),
		DBPath:              getEnv("DB_PATH", "data/atlas.sqlite"),
		Timezone:            tzName,
		Location:            loc,
		HTTPPort:            getEnv("PORT", getEnv("HTTP_PORT", "8080")),
		BackupHour:          getEnv("BACKUP_HOUR", "03:00"),
		CalendarToken:       getEnv("CALENDAR_TOKEN", "atlas_cal_secret_token"),
		PublicURL:           strings.TrimSuffix(getEnv("PUBLIC_URL", "http://localhost:8080"), "/"),
		MaxTokensQuick:      maxQuick,
		MaxTokensHeavy:      maxHeavy,
		MaxTokensResearch:   maxResearch,
		HistoryLimit:        histLimit,
		TursoDatabaseURL:    getEnv("TURSO_DATABASE_URL", ""),
		TursoAuthToken:      getEnv("TURSO_AUTH_TOKEN", ""),
		CronSecret:          getEnv("CRON_SECRET", ""),
		SonarBridgeURL:      getEnv("SONAR_BRIDGE_URL", ""),
		SonarBridgeToken:    getEnv("SONAR_BRIDGE_TOKEN", "Li3cht3nst3in2"),
		SonarCLIPath:        getEnv("SONAR_CLI_PATH", "/home/chocotonilitz/Programacao/projetos/sonar/sonar_bridge.php"),
		ProjectsDir:         getEnv("PROJECTS_BASE_DIR", "/home/chocotonilitz/Programacao/projetos"),
		IMAPHost:            getEnv("IMAP_HOST", ""),
		IMAPPort:            getEnv("IMAP_PORT", "993"),
		IMAPUser:            getEnv("IMAP_USER", ""),
		IMAPPass:            getEnv("IMAP_PASS", ""),
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
