package client

import (
	"crypto/tls"
	"flag"
	"hsync/internal/utils"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	ServerURL          string `toml:"server"`
	Key                string `toml:"key"`
	DirPath            string `toml:"dir"`
	StatePath          string `toml:"state"`
	Interval           string `toml:"interval"`
	Timeout            string `toml:"timeout"`
	InsecureSkipVerify bool   `toml:"insecureSkipVerify"`

	caseInsensitive bool
}

func Run(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "Path to configuration file")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Error loading %s: %v", *configPath, err)
	}

	if err := os.MkdirAll(cfg.DirPath, 0755); err != nil {
		log.Fatal(err)
	}
	if cfg.InsecureSkipVerify {
		log.Println("WARNING: TLS certificate verification skipped")
	}
	cfg.caseInsensitive = utils.IsCaseInsensitiveDir(cfg.DirPath)

	st, err := loadState(cfg.StatePath)
	if err != nil {
		log.Fatalf("Error loading sync state from %s: %v", cfg.StatePath, err)
	}
	log.Printf("Syncing %s with %s from commit %s", cfg.DirPath, cfg.ServerURL, short(st.Commit))

	client := httpClient(cfg)
	interval := duration(cfg.Interval, 5*time.Second)

	for {
		runCycle(cfg, client, st)
		time.Sleep(interval)
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if cfg.ServerURL == "" {
		cfg.ServerURL = "http://localhost:8080"
	}
	if cfg.ServerURL, err = normalizeServerURL(cfg.ServerURL); err != nil {
		return nil, err
	}
	if cfg.Key == "" {
		cfg.Key = "default-secret"
	}
	if cfg.DirPath == "" {
		cfg.DirPath = defaultNoteDir()
	}
	if cfg.StatePath == "" {
		cfg.StatePath = defaultStatePath(cfg.DirPath)
	}
	return cfg, nil
}

func httpClient(cfg *Config) *http.Client {
	client := &http.Client{Timeout: duration(cfg.Timeout, 60*time.Second)}
	if cfg.InsecureSkipVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return client
}

func duration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("Invalid duration %q: %v", value, err)
	}
	return parsed
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "hsync.toml"
	}
	return filepath.Join(home, ".config", "hsync.toml")
}

func defaultNoteDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Heynote", "notes")
	case "linux":
		return filepath.Join(home, ".config", "Heynote", "notes")
	default:
		return "."
	}
}

func defaultStatePath(dir string) string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(dir, ".hsync-state.json")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return filepath.Join(stateHome, "hsync", utils.CalculateHash(abs)[:16]+".json")
}
