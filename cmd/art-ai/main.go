package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/LeXwDeX/Plinth/internal/app"
	"github.com/LeXwDeX/Plinth/internal/logging"
)

func main() {
	// Debug until the runtime setting is read, so configuration errors are logged.
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	logger := logging.New(os.Stdout, level)
	config, err := app.LoadConfig()
	if err != nil {
		logger.Error("配置无效", "error", err)
		os.Exit(1)
	}
	level.Set(config.Logging.Level)
	if config.Logging.Directory != "" {
		file, err := logging.OpenRotatingFile(logging.RotateConfig{
			Directory:     config.Logging.Directory,
			FileName:      "art-ai.log",
			MaxBytes:      config.Logging.MaxBytes,
			MaxFiles:      config.Logging.MaxFiles,
			RetentionDays: config.Logging.RetentionDays,
		})
		if err != nil {
			logger.Error("日志目录不可写", "directory", config.Logging.Directory, "error", err)
			os.Exit(1)
		}
		defer file.Close()
		logger = logging.New(&logging.Tee{Primary: os.Stdout, Secondary: file, Report: os.Stderr}, level)
	}
	slog.SetDefault(logger)
	logger.Info("日志已配置", "log_level", config.Logging.Level.String(), "directory", config.Logging.Directory)
	for _, alias := range config.DeprecatedEnvironment {
		logger.Warn("环境变量使用了已弃用的别名，请改用规范名（#252）", "alias", alias)
	}
	if len(config.RetiredEnvironment) > 0 {
		logger.Warn("已忽略退役环境变量，请从运行配置中移除", "variables", config.RetiredEnvironment)
	}
	application, err := app.New(config, logger)
	if err != nil {
		logger.Error("初始化失败", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := application.Run(ctx); err != nil {
		logger.Error("服务异常退出", "error", err)
		os.Exit(1)
	}
}
