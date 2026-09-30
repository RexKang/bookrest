package app

// Version 是软件版本号；发布时可用 -ldflags "-X .../internal/app.Version=x.y.z" 覆盖。
var Version = "0.1.0"

// Version 供界面显示（Logo 右侧的版本号）。
func (a *App) AppVersion() string { return Version }
