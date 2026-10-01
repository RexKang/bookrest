// Package config 管理本机配置与库注册表（%APPDATA%/bookrest/config.json）。
// 配置只存在于本机；库内只放可携带的快照（.bookrest/）。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/RexKang/bookrest/internal/store"
)

type Library struct {
	Root string `json:"root"`
	Name string `json:"name"`
}

// Settings 对应详细设计 §10 的配置项。
type Settings struct {
	MirrorDir      string `json:"mirror_dir"`
	WriteBack      bool   `json:"write_back_enabled"`
	SyncIndex      bool   `json:"sync_index"`
	SyncThumbs     bool   `json:"sync_thumbs"`
	SyncDebounceMS int    `json:"sync_debounce_ms"`
	ScanWorkers    int    `json:"scan_workers"`
	PruneEnabled   bool   `json:"prune_enabled"`
	FullVerifyDays int    `json:"full_verify_days"`
	// Proxy 是联网补全信息时的代理："" = 跟随系统，direct = 直连，或 http://host:port
	Proxy        string `json:"proxy,omitempty"`
	Theme        string `json:"theme,omitempty"`
	SpineStyle     string `json:"spine_style"`
	CacheMaxBytes  int64  `json:"cache_max_bytes"`
	OpenWithMap    map[string]string `json:"external_open_map"`
}

type Config struct {
	V         int       `json:"v"`
	Libraries []Library `json:"libraries"`
	Active    string    `json:"active"`
	Settings  Settings  `json:"settings"`
}

// AppDir 返回本机应用目录（%APPDATA%/bookrest）。
func AppDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "bookrest")
}

func Default() *Config {
	dir := AppDir()
	return &Config{
		V: 1,
		Settings: Settings{
			MirrorDir:      filepath.Join(dir, "mirror"),
			WriteBack:      true,
			SyncIndex:      true,
			SyncThumbs:     false,
			SyncDebounceMS: 5000,
			ScanWorkers:    4,
			PruneEnabled:   true,
			FullVerifyDays: 7,
			Theme:          "dark",
			SpineStyle:     "wood",
			CacheMaxBytes:  512 << 20,
			OpenWithMap:    map[string]string{},
		},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		// 配置损坏：退回默认值，不阻塞启动（配置可从界面重建）
		return Default(), nil
	}
	def := Default()
	if c.Settings.MirrorDir == "" {
		c.Settings.MirrorDir = def.Settings.MirrorDir
	}
	if c.Settings.SyncDebounceMS == 0 {
		c.Settings.SyncDebounceMS = def.Settings.SyncDebounceMS
	}
	if c.Settings.ScanWorkers == 0 {
		c.Settings.ScanWorkers = def.Settings.ScanWorkers
	}
	if c.Settings.CacheMaxBytes == 0 {
		c.Settings.CacheMaxBytes = def.Settings.CacheMaxBytes
	}
	if c.Settings.OpenWithMap == nil {
		c.Settings.OpenWithMap = map[string]string{}
	}
	if c.Settings.Theme == "" {
		c.Settings.Theme = def.Settings.Theme
	}
	return &c, nil
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(path, data)
}

// AddLibrary 注册一个库（去重）。
func (c *Config) AddLibrary(root, name string) {
	for i, l := range c.Libraries {
		if samePath(l.Root, root) {
			c.Libraries[i].Name = name
			return
		}
	}
	c.Libraries = append(c.Libraries, Library{Root: root, Name: name})
}

func (c *Config) RemoveLibrary(root string) {
	out := c.Libraries[:0]
	for _, l := range c.Libraries {
		if !samePath(l.Root, root) {
			out = append(out, l)
		}
	}
	c.Libraries = out
	if samePath(c.Active, root) {
		c.Active = ""
	}
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
