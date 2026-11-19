// Command easycloud is the NAS file manager server.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/envfile"
	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/logfile"
	"github.com/easycloud/easycloud/internal/server"
)

// Exit codes understood by systemd/docker restart policies.
const (
	exitUnavailable = 69 // transient: keep restarting
	exitConfig      = 78 // configuration error: do not restart
)

var version = "dev"

const usage = `usage: easycloud <command>

commands:
  serve                     start the server (default)
  install [--env FILE] [-y]  interactive database check used by install.sh
  rebind                    accept the current data root for this database
  reset-password <user>     set a new password (prompted, or EASYCLOUD_NEW_PASSWORD)
  version                   print the version
`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "rebind":
		err = rebind()
	case "install":
		err = install(os.Stdin, os.Stdout)
	case "reset-password":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		err = resetPassword(os.Args[2])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if errors.Is(err, database.ErrConfig) || errors.Is(err, server.ErrConfig) {
		return exitConfig
	}
	return exitUnavailable
}

func newLogger(level, dir string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	var out io.Writer = os.Stdout
	var fileErr error
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			if f, err := logfile.Open(dir, 20<<20, 5); err == nil {
				out = io.MultiWriter(os.Stdout, quietWriter{f})
			} else {
				fileErr = err
			}
		}
	}
	log := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: l}))
	if fileErr != nil {
		log.Warn("log directory is not writable; logging to stdout only", "dir", dir, "err", fileErr,
			"fix", "make the directory writable by the user EasyCloud runs as (docker: chown it to the uid:gid in `user:`)")
	}
	return log
}

// quietWriter keeps a failing log file (disk full, volume gone) from breaking stdout logging.
type quietWriter struct{ w io.Writer }

func (q quietWriter) Write(p []byte) (int, error) { q.w.Write(p); return len(p), nil }

func loadConfig() (*config.Config, *slog.Logger, error) {
	if p := os.Getenv("EASYCLOUD_ENV_FILE"); p != "" {
		vars, err := envfile.Read(p)
		if err != nil {
			return nil, slog.Default(), fmt.Errorf("%w: %v", server.ErrConfig, err)
		}
		for k, v := range vars {
			if _, set := os.LookupEnv(k); !set {
				os.Setenv(k, v)
			}
		}
	}
	cfg, err := config.Load("easycloud.yaml")
	if err != nil {
		return nil, slog.Default(), fmt.Errorf("%w: %v", server.ErrConfig, err)
	}
	log := newLogger(cfg.Log.Level, cfg.Log.Dir)
	slog.SetDefault(log)
	return cfg, log, nil
}

func serve() error {
	cfg, log, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server.Version = version
	srv, err := server.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	srv.Start(ctx)

	httpSrv := &http.Server{Addr: cfg.HTTP.Listen, Handler: srv.Handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTP.Listen, "data_root", cfg.DataRoot, "db", cfg.Database.Redacted(), "version", version)
		errCh <- httpSrv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if strings.Contains(fmt.Sprint(err), "address already in use") {
			return fmt.Errorf("%w: %v", server.ErrConfig, err)
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}

func rebind() error {
	cfg, log, err := loadConfig()
	if err != nil {
		return err
	}
	dataRoot, err := server.PrepareDataRoot(cfg.DataRoot)
	if err != nil {
		return err
	}
	db, err := server.OpenDB(context.Background(), cfg, dataRoot, true, log)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Printf("database is now bound to %s; restart easycloud to rebuild the file index\n", dataRoot)
	return nil
}

func resetPassword(username string) error {
	cfg, log, err := loadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()
	dataRoot, err := server.PrepareDataRoot(cfg.DataRoot)
	if err != nil {
		return err
	}
	// Only the database is needed, so this works while the service is running.
	db, err := server.OpenDB(ctx, cfg, dataRoot, false, log)
	if err != nil {
		return err
	}
	defer db.Close()
	root, err := os.OpenRoot(dataRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	ids := identity.NewService(db, root, log)
	pw := os.Getenv("EASYCLOUD_NEW_PASSWORD")
	if pw == "" {
		fmt.Printf("new password for %s: ", username)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	users, err := ids.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Username == username {
			if err := ids.SetPassword(ctx, u.ID, pw, ""); err != nil {
				return err
			}
			fmt.Println("password updated; existing sessions were signed out")
			return nil
		}
	}
	return fmt.Errorf("%w: no user named %q", server.ErrConfig, username)
}
