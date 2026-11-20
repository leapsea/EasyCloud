// Package server is the composition root: it wires every module into one HTTP handler.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/filetoken"
	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/index"
	"github.com/easycloud/easycloud/internal/media/info"
	"github.com/easycloud/easycloud/internal/media/playback"
	"github.com/easycloud/easycloud/internal/monitor"
	"github.com/easycloud/easycloud/internal/office"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/eventbus"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/plugin"
	"github.com/easycloud/easycloud/internal/realtime"
	"github.com/easycloud/easycloud/internal/settings"
	"github.com/easycloud/easycloud/internal/share"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"github.com/easycloud/easycloud/internal/storage/infra"
	storagehttp "github.com/easycloud/easycloud/internal/storage/transport"
	"github.com/easycloud/easycloud/internal/thumb"
	"github.com/easycloud/easycloud/internal/transfer"
	"github.com/easycloud/easycloud/internal/webdav"
	"github.com/easycloud/easycloud/pkg/pluginsdk"
	"github.com/easycloud/easycloud/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Version is set by main from the build-time version string.
var Version = "dev"

// ErrConfig marks configuration problems (exit 78).
var ErrConfig = errors.New("configuration error")

type Server struct {
	Handler  http.Handler
	DB       *database.DB
	Identity *identity.Service
	Files    *storageapp.FileService
	Indexer  *index.Indexer
	Plugins  *plugin.Manager

	root       *os.Root
	background []func(context.Context)
	closers    []func()
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// PrepareDataRoot validates the data root and its reserved directory.
func PrepareDataRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: data_root %q is not a directory", ErrConfig, abs)
	}
	reserved := filepath.Join(abs, pathguard.ReservedDir)
	if err := os.MkdirAll(reserved, 0o700); err != nil {
		return "", fmt.Errorf("%w: cannot create %s: %v", ErrConfig, reserved, err)
	}
	var a, b syscall.Stat_t
	if err := syscall.Stat(abs, &a); err != nil {
		return "", err
	}
	if err := syscall.Stat(reserved, &b); err != nil {
		return "", err
	}
	if a.Dev != b.Dev {
		return "", fmt.Errorf("%w: %s must be on the same filesystem as %s (atomic trash/upload renames)", ErrConfig, reserved, abs)
	}
	return abs, nil
}

// OpenDB opens, checks and migrates the database and binds it to the data root.
func OpenDB(ctx context.Context, cfg *config.Config, dataRoot string, rebind bool, log *slog.Logger) (*database.DB, error) {
	db, err := database.Open(ctx, cfg.Database, cfg.Paths.AppData, log)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.BindInstance(ctx, dataRoot, rebind); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Server, error) {
	dataRoot, err := PrepareDataRoot(cfg.DataRoot)
	if err != nil {
		return nil, err
	}
	db, err := OpenDB(ctx, cfg, dataRoot, false, log)
	if err != nil {
		return nil, err
	}
	s := &Server{DB: db}
	s.closers = append(s.closers, func() { db.Close() })
	root, err := os.OpenRoot(dataRoot)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.root = root
	s.closers = append(s.closers, func() { root.Close() })

	set := settings.Resolver{Store: settings.NewSQLStore(db), Cfg: cfg}
	rec := audit.NewRecorder(db, log)
	ids := identity.NewService(db, root, log)
	if err := ids.EnsureAdmin(ctx, cfg.Admin.Password); err != nil {
		s.Close()
		return nil, err
	}
	guard := pathguard.NewGrantAuthorizer(root, ids)
	bus := eventbus.New[storageapp.Event]()
	files := storageapp.NewFileService(guard, root, infra.NewTrashRepo(db), bus, storageapp.SystemClock{}, set, log)
	files.AddObserver(ids)
	files.SetProtector(protector{ids: ids})
	idxStore := index.NewStore(db)
	go idxStore.Backfill(ctx, log)
	files.SetQuota(quota{ids: ids, store: idxStore})
	upload, err := transfer.NewService(dataRoot, files, set, rec, log)
	if err != nil {
		s.Close()
		return nil, err
	}
	hub := realtime.NewHub(guard, log)
	s.Identity, s.Files = ids, files

	trusted, err := plugin.ParseKeys(cfg.Plugins.TrustedKeys)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	plugins := plugin.NewManager(db, plugin.Options{
		Dir: filepath.Join(cfg.Paths.AppData, "plugins"), HostVersion: Version,
		AllowUnsigned: cfg.Plugins.AllowUnsigned, TrustedKeys: append(plugin.OfficialKeys(), trusted...), Repository: cfg.Plugins.Repository,
	}, log)
	if err := plugins.Load(ctx); err != nil {
		s.Close()
		return nil, err
	}
	s.Plugins = plugins
	thumbs := thumb.New(guard, dataRoot, filepath.Join(cfg.Paths.AppData, "thumbs"), plugins, log)
	player := playback.New(files, filepath.Join(cfg.Paths.AppData, "playback"), rec, log)
	files.SetEnricher(enricher{store: idxStore, thumbs: thumbs})
	signer, err := filetoken.Load(ctx, db)
	if err != nil {
		s.Close()
		return nil, err
	}
	officeSvc := office.New(plugins, files, ids, signer, rec, log)
	shares := share.NewService(db, files, ids)
	files.AddObserver(shares)
	dav := webdav.NewService(db, root)
	files.AddObserver(dav)
	shareHTTP := share.NewHTTP(shares, signer, thumbs, rec, log)
	tasks := transfer.NewTaskManager(db, files,
		func(ctx context.Context, uid string) (pathguard.Subject, bool) {
			u, err := ids.GetUser(ctx, uid)
			return u.Subject(), err == nil && !u.Disabled
		},
		func(ctx context.Context, p pathguard.RelPath) int64 {
			b, _, _ := idxStore.Usage(ctx, p.String())
			return b
		},
		func(uid string, t transfer.Task) { hub.Broadcast("tasks:"+uid, t) }, log)
	tasks.SetVideoFixer(player.SaveFixed)
	plugins.SetAccountSource(func(ctx context.Context) (*pluginsdk.SyncRequest, error) {
		req, err := ids.ProtocolSnapshot(ctx)
		if req != nil {
			req.DataRoot = dataRoot
		}
		return req, err
	})
	ids.OnChange(debounce(2*time.Second, func() { plugins.SyncAccounts(context.Background()) }))
	indexer := index.New(root, dataRoot, idxStore, index.Hooks{
		Changed: func(dirs []string) { bus.Publish(storageapp.Event{Kind: "changed", Dirs: dirs}) },
		Moved: func(ctx context.Context, from, to pathguard.RelPath) {
			ids.PathMoved(ctx, from, to)
			shares.PathMoved(ctx, from, to)
			dav.PathMoved(ctx, from, to)
		},
	}, log)
	s.Indexer = indexer

	events, unsub := bus.Subscribe(256)
	s.closers = append(s.closers, unsub)
	go func() {
		for ev := range events {
			// One event per directory topic, so a subscriber never learns about other directories.
			for _, d := range ev.Dirs {
				hub.Broadcast("dir:"+d, storageapp.Event{Kind: ev.Kind, Dirs: []string{d}})
			}
			plugins.Broadcast(pluginsdk.FileEvent{Kind: ev.Kind, Dirs: ev.Dirs})
		}
	}()

	metricStore, err := monitor.OpenPebble(filepath.Join(cfg.Paths.AppData, "metrics"))
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("open metrics store: %w", err)
	}
	s.closers = append(s.closers, func() { metricStore.Close() })
	collector := monitor.NewCollector(metricStore, dataRoot, func(ctx context.Context) (time.Duration, time.Duration) {
		raw, _, _ := set.Int(ctx, settings.MetricsRawDays)
		minute, _, _ := set.Int(ctx, settings.MetricsMinuteDays)
		return time.Duration(raw) * 24 * time.Hour, time.Duration(minute) * 24 * time.Hour
	}, func(sample map[string]float64) { hub.Broadcast("metrics", sample) }, log)
	systemExtra := func(r *http.Request) map[string]any {
		users, _ := ids.ListUsers(r.Context())
		top, _ := idxStore.TopLevel(r.Context())
		total, files, _ := idxStore.Usage(r.Context(), ".")
		return map[string]any{
			"app":     map[string]any{"version": Version, "database": cfg.Database.Redacted(), "dataRoot": dataRoot},
			"users":   len(users),
			"storage": map[string]any{"indexedBytes": total, "indexedFiles": files, "topLevel": top},
		}
	}

	idHTTP := identity.NewHTTP(ids, rec, log)
	for _, m := range webdav.Methods {
		chi.RegisterMethod(m)
	}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, httpx.Logging(log), httpx.SameOrigin, securityHeaders)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	plugin.MountStatic(r, plugins)
	r.Handle(webdav.Prefix+"/*", webdav.Handler(dav, files, root, rec, log))
	r.Route("/api", func(api chi.Router) {
		idHTTP.MountPublic(api)
		mountRaw(api, signer, ids, files, log)
		officeSvc.MountPublic(api, log)
		shareHTTP.MountPublic(api)
		api.Group(func(p chi.Router) {
			p.Use(idHTTP.Require)
			idHTTP.Mount(p)
			storagehttp.Mount(p, files, rec, log)
			playback.Mount(p, player, log)
			info.Mount(p, info.New(files, log), log)
			index.Mount(p, idxStore, guard, log)
			index.MountCategories(p, idxStore, guard, thumbs.Supported, log)
			thumb.Mount(p, thumbs, log)
			mountTokens(p, signer, files, log)
			plugin.Mount(p, plugins, log)
			officeSvc.Mount(p, log)
			shareHTTP.Mount(p)
			tasks.MountTasks(p, log)
			set.Mount(p, log)
			p.Handle("/upload", upload.Handler())
			p.Handle("/upload/*", upload.Handler())
			p.Handle("/ws", hub)
			p.Route("/admin", func(a chi.Router) {
				a.Use(httpx.RequireAdmin(log))
				idHTTP.MountAdmin(a)
				monitor.MountAdmin(a, metricStore, collector, systemExtra, log)
				shareHTTP.MountAdmin(a)
				webdav.MountAdmin(a, dav, rec, log)
				plugin.MountAdmin(a, plugins, plugin.HTTPFetcher, func(r *http.Request, action, detail string) {
					sub := httpx.Subject(r)
					rec.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: action, Detail: detail, IP: httpx.ClientIP(r)})
				}, log)
			})
		})
		api.NotFound(func(w http.ResponseWriter, r *http.Request) { httpx.WriteError(w, r, log, errNotFoundRoute) })
	})
	r.Handle("/*", web.SPA())
	s.Handler = r

	s.background = append(s.background,
		rec.Run,
		indexer.Run,
		collector.Run,
		plugins.Run,
		tasks.Run,
		files.StartTrashReaper,
		upload.StartJanitor,
		every(time.Hour, func(ctx context.Context) {
			ids.PurgeExpiredSessions(ctx)
			thumbs.Prune(180 * 24 * time.Hour)
			player.Prune(90 * 24 * time.Hour)
			shares.PruneExpired(ctx)
			rec.Prune(ctx, 365*24*time.Hour)
		}),
	)
	return s, nil
}

var errNotFoundRoute = errorsNew404()

// Start launches background workers; they stop when ctx is cancelled or Close is called.
func (s *Server) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, fn := range s.background {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			fn(ctx)
		}()
	}
}

// Close stops background workers first, then releases stores, so no worker
// ever touches a closed database.
func (s *Server) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
}

func every(d time.Duration, fn func(context.Context)) func(context.Context) {
	return func(ctx context.Context) {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn(ctx)
			}
		}
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// debounce coalesces bursts of calls (e.g. editing several users) into one.
func debounce(d time.Duration, fn func()) func() {
	var mu sync.Mutex
	var t *time.Timer
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
		}
		t = time.AfterFunc(d, fn)
	}
}
