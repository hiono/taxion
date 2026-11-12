package runtime

import "github.com/hiono/taxion/internal/core"

type UsageCheckpoint = core.UsageCheckpoint

func DecodeUsageCheckpoint(payload, sessionID string, calls []core.DispatchUsage) (UsageCheckpoint, bool, error) {
	return core.DecodeUsageCheckpoint(payload, sessionID, calls)
}
