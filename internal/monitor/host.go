package monitor

import (
	"context"

	"github.com/shirou/gopsutil/v4/host"
)

func hostUptime(ctx context.Context) (uint64, string) {
	i, err := host.InfoWithContext(ctx)
	if err != nil {
		return 0, ""
	}
	return i.Uptime, i.Platform + " " + i.PlatformVersion
}
