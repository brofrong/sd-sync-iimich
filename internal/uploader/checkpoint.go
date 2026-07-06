package uploader

import (
	"sync"
	"sync/atomic"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

type syncCheckpoint struct {
	state    *state.DB
	session  *syncSession
	interval int
	saved    atomic.Int64
	mu       sync.Mutex
}

func newSyncCheckpoint(st *state.DB, session *syncSession, interval int) *syncCheckpoint {
	return &syncCheckpoint{state: st, session: session, interval: interval}
}

func (c *syncCheckpoint) onFileSaved() {
	n := c.saved.Add(1)
	if int(n)%c.interval == 0 {
		c.flushCount(n)
	}
}

func (c *syncCheckpoint) flush() {
	c.flushCount(c.saved.Load())
}

func (c *syncCheckpoint) flushCount(saved int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.state.Checkpoint(); err != nil {
		logx.Warn("sync: checkpoint failed", "err", err)
		return
	}

	cur := c.session.watermark()
	logx.Info("sync: checkpoint",
		"saved", saved,
		"last_mtime", cur.Mtime,
		"last_file", cur.RelativePath,
	)
}
