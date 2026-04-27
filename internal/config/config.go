package config

import (
	"errors"
	"fmt"
	"os"

	"deploy/internal/pipeline"

	"gopkg.in/yaml.v3"
)

// File is the YAML schema. Field names mirror pipeline.Config for clarity.
type File struct {
	Host     string      `yaml:"host"`
	Port     int         `yaml:"port"`
	User     string      `yaml:"user"`
	Password string      `yaml:"password"`
	KeyFile  string      `yaml:"key"`
	Pre      string      `yaml:"pre"`
	Backup   [][2]string `yaml:"backup"`
	Delete   []string    `yaml:"delete"`
	Upload   [][2]string `yaml:"upload"`
	Extract  [][2]string `yaml:"extract"`
	Post     string      `yaml:"post"`
}

// DefaultPaths are the file names probed when --config is not given.
var DefaultPaths = []string{"deploy.yaml", "deploy.yml"}

// Load reads a YAML config file and returns a pipeline.Config.
// If path is empty, it probes DefaultPaths; if none exists, it returns an
// empty Config without error so callers can fall back to CLI flags only.
func Load(path string) (pipeline.Config, error) {
	if path == "" {
		for _, p := range DefaultPaths {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		return pipeline.Config{}, nil // no config file — that's fine
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pipeline.Config{}, fmt.Errorf("config file not found: %s", path)
		}
		return pipeline.Config{}, fmt.Errorf("read config: %w", err)
	}

	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return pipeline.Config{}, fmt.Errorf("parse config: %w", err)
	}

	port := f.Port
	if port == 0 {
		port = 22
	}

	return pipeline.Config{
		Host:         f.Host,
		Port:         port,
		User:         f.User,
		Password:     f.Password,
		KeyFile:      f.KeyFile,
		PreCmd:       f.Pre,
		BackupPaths:  f.Backup,
		DeletePaths:  f.Delete,
		UploadPaths:  f.Upload,
		ExtractPaths: f.Extract,
		PostCmd:      f.Post,
	}, nil
}
