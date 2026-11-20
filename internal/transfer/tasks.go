package transfer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/storage/app"
)

// Task kinds.
const (
	TaskCopy    = "copy"
	TaskMove    = "move"
	TaskExtract = "extract"
	// TaskVideoFix saves a playback-fixed copy of a video next to it (see
	// internal/media/playback); available once SetVideoFixer is called.
	TaskVideoFix = "videofix"
)

// VideoFixer writes the fixed copy of a video and returns its path.
type VideoFixer func(ctx context.Context, sub pathguard.Subject, path string) (string, error)

type TaskParams struct {
	Sources    []string           `json:"sources"`
	Dest       string             `json:"dest,omitempty"`
	OnConflict app.ConflictPolicy `json:"onConflict,omitempty"`
}

type Task struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"-"`
	Kind       string     `json:"kind"`
	Params     TaskParams `json:"params"`
	Status     string     `json:"status"` // queued | running | done | failed | cancelled
	DoneBytes  int64      `json:"doneBytes"`
	TotalBytes int64      `json:"totalBytes"`
	Message    string     `json:"message,omitempty"`
	Result     []string   `json:"result,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// SubjectLookup resolves the task owner when the task runs (they may be disabled meanwhile).
type SubjectLookup func(ctx context.Context, userID string) (pathguard.Subject, bool)

// SizeEstimator estimates bytes under a path (from the file index).
type SizeEstimator func(ctx context.Context, p pathguard.RelPath) int64

type TaskManager struct {
	db      *database.DB
	files   *app.FileService
	subject SubjectLookup
	size    SizeEstimator
	notify  func(userID string, t Task)
	log     *slog.Logger

	videoFix VideoFixer

	queue   chan string
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewTaskManager(db *database.DB, files *app.FileService, subject SubjectLookup, size SizeEstimator, notify func(string, Task), log *slog.Logger) *TaskManager {
	return &TaskManager{db: db, files: files, subject: subject, size: size, notify: notify, log: log,
		queue: make(chan string, 1024), cancels: map[string]context.CancelFunc{}}
}

const taskCols = "id, owner_id, kind, params, status, done_bytes, total_bytes, message, result, created_at, updated_at"

func scanTask(r interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var params, result string
	var c, u int64
	err := r.Scan(&t.ID, &t.OwnerID, &t.Kind, &params, &t.Status, &t.DoneBytes, &t.TotalBytes, &t.Message, &result, &c, &u)
	_ = json.Unmarshal([]byte(params), &t.Params)
	if result != "" {
		_ = json.Unmarshal([]byte(result), &t.Result)
	}
	t.CreatedAt, t.UpdatedAt = time.Unix(c, 0).UTC(), time.Unix(u, 0).UTC()
	return t, err
}

func (m *TaskManager) get(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(m.db.R.QueryRowContext(ctx, "SELECT "+taskCols+" FROM easycloud_tasks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, errs.ErrNotFound
	}
	return t, err
}

// Create validates and enqueues a task.
// SetVideoFixer enables the videofix task kind.
func (m *TaskManager) SetVideoFixer(f VideoFixer) { m.videoFix = f }

func (m *TaskManager) Create(ctx context.Context, sub pathguard.Subject, kind string, p TaskParams) (Task, error) {
	switch kind {
	case TaskCopy, TaskMove:
		if len(p.Sources) == 0 || len(p.Sources) > 1000 {
			return Task{}, errs.ErrInvalidInput.WithDetail("sources must contain 1..1000 items")
		}
		if _, err := m.files.Stat(ctx, sub, p.Dest); err != nil {
			return Task{}, err
		}
	case TaskExtract:
		if len(p.Sources) != 1 || app.ArchiveKind(p.Sources[0]) == "" {
			return Task{}, errs.ErrInvalidInput.WithDetail("extract needs exactly one zip/tar/tar.gz file")
		}
	case TaskVideoFix:
		if m.videoFix == nil {
			return Task{}, errs.ErrInvalidInput.WithDetail("unknown task kind")
		}
		if len(p.Sources) != 1 {
			return Task{}, errs.ErrInvalidInput.WithDetail("videofix needs exactly one video")
		}
	default:
		return Task{}, errs.ErrInvalidInput.WithDetail("unknown task kind")
	}
	for _, s := range p.Sources { // fail fast on paths the user cannot even read
		if _, err := m.files.Stat(ctx, sub, s); err != nil {
			return Task{}, err
		}
	}
	if p.OnConflict == "" {
		p.OnConflict = app.ConflictRename
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	now := time.Now().UTC()
	t := Task{ID: hex.EncodeToString(b), OwnerID: sub.UserID, Kind: kind, Params: p, Status: "queued", CreatedAt: now, UpdatedAt: now}
	params, _ := json.Marshal(p)
	if _, err := m.db.W.ExecContext(ctx, "INSERT INTO easycloud_tasks ("+taskCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		t.ID, t.OwnerID, t.Kind, string(params), t.Status, 0, 0, "", "", now.Unix(), now.Unix()); err != nil {
		return Task{}, err
	}
	select {
	case m.queue <- t.ID:
	default:
		return Task{}, errs.New(503, "TASK_QUEUE_FULL")
	}
	m.notify(t.OwnerID, t)
	return t, nil
}

func (m *TaskManager) List(ctx context.Context, sub pathguard.Subject) ([]Task, error) {
	rows, err := m.db.R.QueryContext(ctx, "SELECT "+taskCols+" FROM easycloud_tasks WHERE owner_id = ? ORDER BY created_at DESC LIMIT 50", sub.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Cancel stops a queued or running task owned by sub.
func (m *TaskManager) Cancel(ctx context.Context, sub pathguard.Subject, id string) error {
	t, err := m.get(ctx, id)
	if err != nil || t.OwnerID != sub.UserID {
		return errs.ErrNotFound
	}
	m.mu.Lock()
	if cancel, ok := m.cancels[id]; ok {
		cancel()
	}
	m.mu.Unlock()
	if t.Status == "queued" {
		m.finish(context.Background(), t, "cancelled", "", nil)
	}
	return nil
}

// ClearFinished removes the owner's finished tasks.
func (m *TaskManager) ClearFinished(ctx context.Context, sub pathguard.Subject) error {
	_, err := m.db.W.ExecContext(ctx, "DELETE FROM easycloud_tasks WHERE owner_id = ? AND status IN ('done','failed','cancelled')", sub.UserID)
	return err
}

func (m *TaskManager) update(ctx context.Context, t Task) {
	t.UpdatedAt = time.Now().UTC()
	result, _ := json.Marshal(t.Result)
	_, err := m.db.W.ExecContext(ctx, "UPDATE easycloud_tasks SET status = ?, done_bytes = ?, total_bytes = ?, message = ?, result = ?, updated_at = ? WHERE id = ?",
		t.Status, t.DoneBytes, t.TotalBytes, t.Message, string(result), t.UpdatedAt.Unix(), t.ID)
	if err != nil {
		m.log.Warn("task update", "id", t.ID, "err", err)
	}
	m.notify(t.OwnerID, t)
}

func (m *TaskManager) finish(ctx context.Context, t Task, status, msg string, result []string) {
	t.Status, t.Message, t.Result = status, msg, result
	if status == "done" && t.TotalBytes > 0 {
		t.DoneBytes = t.TotalBytes
	}
	m.update(ctx, t)
}

// Run recovers interrupted tasks, then processes the queue with two workers.
func (m *TaskManager) Run(ctx context.Context) {
	if _, err := m.db.W.ExecContext(ctx, "UPDATE easycloud_tasks SET status = 'failed', message = 'interrupted by a restart', updated_at = ? WHERE status IN ('queued','running')", time.Now().Unix()); err != nil {
		m.log.Warn("task recovery", "err", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-m.queue:
					m.execute(ctx, id)
				}
			}
		}()
	}
	wg.Wait()
}

func (m *TaskManager) execute(parent context.Context, id string) {
	t, err := m.get(parent, id)
	if err != nil || t.Status != "queued" {
		return
	}
	sub, ok := m.subject(parent, t.OwnerID)
	if !ok {
		m.finish(parent, t, "failed", "owner is no longer active", nil)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
	}()

	for _, s := range t.Params.Sources {
		if p, err := pathguard.Parse(s); err == nil && m.size != nil {
			t.TotalBytes += m.size(ctx, p)
		}
	}
	t.Status = "running"
	m.update(ctx, t)

	var done atomic.Int64
	stop := make(chan struct{})
	go func() { // throttle progress updates to every 500ms
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		last := int64(-1)
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if d := done.Load(); d != last {
					last = d
					t.DoneBytes = d
					m.update(context.Background(), t)
				}
			}
		}
	}()
	pctx := app.WithProgress(ctx, func(n int64) { done.Add(n) })

	var result []string
	switch t.Kind {
	case TaskCopy:
		result, err = m.files.Copy(pctx, sub, t.Params.Sources, t.Params.Dest, t.Params.OnConflict)
	case TaskMove:
		result, err = m.files.Move(pctx, sub, t.Params.Sources, t.Params.Dest, t.Params.OnConflict)
	case TaskExtract:
		var dest string
		dest, err = m.files.Extract(pctx, sub, t.Params.Sources[0])
		if dest != "" {
			result = []string{dest}
		}
	case TaskVideoFix:
		var dest string
		dest, err = m.videoFix(pctx, sub, t.Params.Sources[0])
		if dest != "" {
			result = []string{dest}
		}
	}
	close(stop)
	t.DoneBytes = done.Load()
	switch {
	case errors.Is(err, context.Canceled) || ctx.Err() != nil && parent.Err() == nil:
		m.finish(context.Background(), t, "cancelled", "", result)
	case err != nil:
		msg := err.Error()
		if ae, ok := errs.As(err); ok {
			msg = ae.Code
			if d, ok := ae.Detail.(string); ok {
				msg += ": " + d
			}
		}
		m.finish(context.Background(), t, "failed", msg, result)
	default:
		m.finish(context.Background(), t, "done", "", result)
	}
}
