package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Token     string   `json:"token"`
	OwnerID   int64    `json:"owner_id"`
	PCName    string   `json:"pc_name"`
	PCMAC     string   `json:"pc_mac"`
	PCIP      string   `json:"pc_ip"`
	Broadcast string   `json:"broadcast"`
	WakeCmd   []string `json:"wake_cmd"`
	Listen    string   `json:"listen"`
	PCKey     string   `json:"pc_key"`
	DataDir   string   `json:"data_dir"`
	LogFile   string   `json:"log_file"`
	Timezone  string   `json:"timezone"`
	TGAPI     string   `json:"tg_api"`
	TGProxy   string   `json:"tg_proxy"`

	path string
	mac  net.HardwareAddr
	loc  *time.Location
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.path = path
	if c.PCName == "" {
		c.PCName = "ПК"
	}
	if c.DataDir == "" {
		c.DataDir = filepath.Dir(path)
	}
	if c.TGAPI == "" {
		c.TGAPI = "https://api.telegram.org"
	}
	if c.Listen == "" {
		c.Listen = "192.168.1.1:8765"
	}
	if c.Timezone == "" {
		c.Timezone = "Europe/Moscow"
	}
	if c.loc, err = time.LoadLocation(c.Timezone); err != nil {
		return nil, fmt.Errorf("config: timezone: %w", err)
	}
	if c.mac, err = net.ParseMAC(c.PCMAC); err != nil {
		return nil, fmt.Errorf("config: pc_mac: %w", err)
	}
	if c.Broadcast == "" {
		return nil, errors.New("config: broadcast is empty")
	}
	if c.OwnerID == 0 {
		return nil, errors.New("config: owner_id is empty")
	}
	if len(c.PCKey) < 32 {
		return nil, errors.New("config: pc_key must be at least 32 characters")
	}
	return c, nil
}

func (c *Config) saveToken(token string) error {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m["token"] = token
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.path, append(out, '\n'), 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
