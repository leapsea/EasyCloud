package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/envfile"
	"github.com/easycloud/easycloud/internal/server"
)

const defaultEnvFile = "/etc/easycloud/.env"

// errUserExit means the operator chose "exit"; install.sh must not install the unit.
var errUserExit = fmt.Errorf("%w: installation cancelled", server.ErrConfig)

// install is the interactive database check run by install.sh. The fallback to
// SQLite is offered only here: the running service never falls back.
func install(in io.Reader, out io.Writer) error {
	envPath := defaultEnvFile
	interactive := true
	for i := 2; i < len(os.Args); i++ {
		switch a := os.Args[i]; {
		case a == "--env" && i+1 < len(os.Args):
			envPath = os.Args[i+1]
			i++
		case strings.HasPrefix(a, "--env="):
			envPath = strings.TrimPrefix(a, "--env=")
		case a == "--non-interactive" || a == "-y":
			interactive = false
		}
	}
	return runInstall(envPath, interactive, in, out)
}

func runInstall(envPath string, interactive bool, in io.Reader, out io.Writer) error {
	rd := bufio.NewReader(in)
	ask := func(prompt string) string {
		fmt.Fprint(out, prompt)
		s, _ := rd.ReadString('\n')
		return strings.TrimSpace(s)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for {
		// 1. Read the .env (re-read on every retry so edits take effect).
		vars, err := envfile.Read(envPath)
		if err != nil {
			return fmt.Errorf("%w: cannot read %s: %v", server.ErrConfig, envPath, err)
		}
		for _, k := range os.Environ() {
			if strings.HasPrefix(k, "EASYCLOUD_") {
				os.Unsetenv(strings.SplitN(k, "=", 2)[0])
			}
		}
		envfile.Apply(vars)
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("%w: %v", server.ErrConfig, err)
		}
		cfg.Database.ConnectTimeout = 5

		// 2/3. Connect (SQLite needs no network check).
		if cfg.Database.Driver == "sqlite" {
			fmt.Fprintf(out, "数据库：SQLite（%s）\n", database.SQLitePath(cfg.Paths.AppData))
		} else {
			fmt.Fprintf(out, "数据库：%s，正在测试连接…\n", cfg.Database.Redacted())
		}
		err = checkDatabase(cfg, log)
		if err == nil {
			// 4. Existing-data compatibility check (migrations, version, instance binding).
			if err := compatibility(cfg, log, out); err != nil {
				return err
			}
			fmt.Fprintln(out, "✔ 数据库检查通过")
			return nil
		}
		fmt.Fprintf(out, "✘ 连接失败：%v\n", reason(err))
		if cfg.Database.Driver == "sqlite" || !interactive {
			return err
		}
		fmt.Fprintf(out, "\n请选择：\n  1) 重试（先修改 %s，然后按回车）\n  2) 改用 SQLite\n  3) 退出（不安装服务）\n", envPath)
		switch ask("选择 [1/2/3]：") {
		case "1", "":
			ask("修改完成后按回车继续…")
		case "2":
			if strings.ToLower(ask("确认改用 SQLite？已配置的 MariaDB 不会被使用 [y/N]：")) != "y" {
				continue
			}
			if err := envfile.Set(envPath, "EASYCLOUD_DB_DRIVER", "sqlite"); err != nil {
				return err
			}
			fmt.Fprintf(out, "已在 %s 中写入 EASYCLOUD_DB_DRIVER=sqlite\n", envPath)
		default:
			return errUserExit
		}
	}
}

func reason(err error) string {
	s := err.Error()
	for _, p := range []string{database.ErrConfig.Error() + ": ", database.ErrUnavailable.Error() + ": "} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// checkDatabase connects, checks the version and verifies DDL privileges.
func checkDatabase(cfg *config.Config, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database, cfg.Paths.AppData, log)
	if err != nil {
		return err
	}
	defer db.Close()
	if db.Dialect == database.MySQL {
		// Privilege probe: migrations need CREATE/ALTER/DROP.
		if _, err := db.W.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS easycloud_install_probe (id INT)"); err != nil {
			return fmt.Errorf("%w: the database user cannot create tables: %v", database.ErrConfig, err)
		}
		_, _ = db.W.ExecContext(ctx, "DROP TABLE easycloud_install_probe")
	}
	return nil
}

func compatibility(cfg *config.Config, log *slog.Logger, out io.Writer) error {
	dataRoot, err := server.PrepareDataRoot(cfg.DataRoot)
	if err != nil {
		return err
	}
	db, err := server.OpenDB(context.Background(), cfg, dataRoot, false, log)
	if err != nil {
		if errors.Is(err, database.ErrConfig) && strings.Contains(err.Error(), "does not match data root") {
			fmt.Fprintf(out, "✘ 数据库属于另一个数据目录。若确认要把该数据库用于 %s，请运行：easycloud rebind\n", dataRoot)
		} else {
			fmt.Fprintf(out, "✘ 已有数据兼容检查未通过：%v\n", reason(err))
		}
		return err
	}
	defer db.Close()
	fmt.Fprintf(out, "✔ 数据目录：%s\n", dataRoot)
	return nil
}
