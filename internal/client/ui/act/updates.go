package act

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/ryoku-dev/ryotunes/internal/client/conn"
	"github.com/ryoku-dev/ryotunes/internal/client/ui/env"
	"github.com/ryoku-dev/ryotunes/internal/model"
	"github.com/ryoku-dev/ryotunes/internal/protocol"
)

var updateCheckStarted atomic.Bool

// updateCheckDelay keeps the check out of the way of startup work.
const updateCheckDelay = 20 * time.Second

// CheckUpdatesOnce asks the daemon, once per client launch, whether a newer
// release exists and points the listener at Settings › About when it does.
// Only the Windows port does this: on Linux, the distribution's package
// manager (or Ryoku) announces updates.
func CheckUpdatesOnce(e *env.Env) {
	if runtime.GOOS != "windows" || e == nil || e.Conn == nil || e.Go == nil {
		return
	}
	if !updateCheckStarted.CompareAndSwap(false, true) {
		return
	}
	e.Go(func(ctx context.Context) {
		select {
		case <-time.After(updateCheckDelay):
		case <-ctx.Done():
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		info, err := conn.Do[protocol.Null, model.UpdateInfo](callCtx, e.Conn, protocol.MethodCheckForUpdates, protocol.Null{})
		if err != nil || !info.Available || info.LatestVersion == nil {
			return
		}
		message := "Ryotunes " + *info.LatestVersion + " is available. Install it from Settings › About."
		dispatch(func() { toast(e, message, env.ToastInfo) })
	})
}
