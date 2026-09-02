package gui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wyhxhh/lanshare/internal/server"
)

// appConfig 界面设置持久化（保存于用户配置目录，随 LanShare 卸载残留可手工删除）。
//
// 注：Password 以明文存于本机用户配置文件中（属个人目录、非共享文件），
// 与多数局域网小工具的存储策略一致；若对安全性有更高要求可后续改为
// Windows DPAPI 加密（CryptProtectData），首版从简。
type appConfig struct {
	Root       string   `json:"root"`
	Port       int      `json:"port"`
	Password   string   `json:"password,omitempty"`
	ShowHidden bool     `json:"show_hidden"`
	Ignore     []string `json:"ignore,omitempty"`
}

// configPath 返回配置文件绝对路径。优先用户配置目录（%APPDATA%），
// 取不到时回退程序所在目录（便携场景）。
func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir, _ = os.Getwd()
	}
	return filepath.Join(dir, "LanShare", "config.json")
}

// loadAppConfig 读取配置；文件不存在或损坏时返回默认值（端口 8000），不报错。
func loadAppConfig() appConfig {
	cfg := appConfig{Port: server.DefaultPort}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	// 损坏时静默回退默认，避免程序起不来
	_ = json.Unmarshal(b, &cfg)
	if cfg.Port < 1 || cfg.Port > 65535 {
		cfg.Port = server.DefaultPort
	}
	return cfg
}

// saveAppConfig 保存配置（目录不存在则创建）。
func saveAppConfig(cfg appConfig) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}
