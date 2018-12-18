package taskrunner

import (
	"context"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	cstructs "github.com/hashicorp/nomad/client/structs"
)

// StatsUpdater is the interface required by the StatsHook to update stats.
// Satisfied by TaskRunner.
type StatsUpdater interface {
	UpdateStats(*cstructs.TaskResourceUsage)
}

// statsHook manages the task stats collection goroutine.
type statsHook struct {
	updater  StatsUpdater
	interval time.Duration

	// cancel is called by Exited or Canceled
	cancel context.CancelFunc

	mu sync.Mutex

	logger hclog.Logger
}

func newStatsHook(su StatsUpdater, interval time.Duration, logger hclog.Logger) *statsHook {
	h := &statsHook{
		updater:  su,
		interval: interval,
	}
	h.logger = logger.Named(h.Name())
	return h
}

func (*statsHook) Name() string {
	return "stats_hook"
}

func (h *statsHook) Poststart(ctx context.Context, req *interfaces.TaskPoststartRequest, _ *interfaces.TaskPoststartResponse) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// This shouldn't happen, but better safe than risk leaking a goroutine
	if h.cancel != nil {
		h.logger.Debug("poststart called twice without exiting between")
		h.cancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	go h.collectResourceUsageStats(ctx, req.DriverStats)

	return nil
}

func (h *statsHook) Exited(context.Context, *interfaces.TaskExitedRequest, *interfaces.TaskExitedResponse) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cancel == nil {
		// No stats running
		return nil
	}

	// Call cancel to stop stats collection
	h.cancel()

	// Clear cancel func so we don't double call for any reason
	h.cancel = nil

	return nil
}

// collectResourceUsageStats starts collecting resource usage stats of a Task.
// Collection ends when the passed channel is closed
func (h *statsHook) collectResourceUsageStats(ctx context.Context, handle interfaces.DriverStats) {

	for {
		ch, err := handle.Stats(ctx)
		if err != nil {
			// Check if the driver doesn't implement stats
			if err.Error() == cstructs.DriverStatsNotImplemented.Error() {
				h.logger.Debug("driver does not support stats")
				return
			}

			continue
		}

		select {
		case ru, ok := <-ch:
			if !ok {
				// Channel closed
			}

			if ru.Err != nil {
				// log err
			}

			// Update stats on TaskRunner and emit them
			h.updater.UpdateStats(ru)

		case <-ctx.Done():
			return
		}
	}
}

func (h *statsHook) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cancel == nil {
		return
	}

	h.cancel()
}
