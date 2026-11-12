package exporterapp

import (
	"fmt"
	"math"
	"syscall"
)

// checksInFlightLimit is how many checks may be in flight at once: half the process's open-file limit, the other half kept for the exporter's own connections and files.
func checksInFlightLimit() (int, error) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return 0, fmt.Errorf("failed to read the open-file limit: %w", err)
	}
	return int(max(min(limit.Cur/2, math.MaxInt32), 1)), nil
}
