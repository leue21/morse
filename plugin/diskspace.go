package plugin

import (
	"context"
	"fmt"
	"sort"
	"syscall"
	"time"
)

// DiskSpace warns before a filesystem fills up.
//
// It exists for the recorder: a stream being written to a full disk is lost and
// cannot be fetched again, which makes running out of space the only failure on
// this host that destroys something irreplaceable. Everything else — analysis,
// cuts, the index — is recomputable.
//
// Both thresholds are checked, and either firing is enough. A percentage alone
// is useless on a large disk, where 5% free is still 95 GB; an absolute floor
// alone is useless on a small one. Whichever is reached first is the one that
// matters.
type DiskSpace struct {
	paths          []string
	minFreePercent float64
	minFreeBytes   uint64
	cooldown       time.Duration

	// usage is swapped in tests. The real one reads the filesystem.
	usage func(path string) (free, total uint64, err error)
	last  map[string]time.Time
}

const gigabyte = 1 << 30

func NewDiskSpace(paths []string, minFreePercent, minFreeGB float64, cooldown time.Duration) *DiskSpace {
	return &DiskSpace{
		paths:          paths,
		minFreePercent: minFreePercent,
		minFreeBytes:   uint64(minFreeGB * gigabyte),
		cooldown:       cooldown,
		usage:          statfsUsage,
		last:           map[string]time.Time{},
	}
}

func (d *DiskSpace) Name() string { return "diskspace" }

func (d *DiskSpace) Describe() string {
	if len(d.paths) == 0 {
		return "disk space: no paths configured"
	}
	var b []string
	for _, path := range d.paths {
		free, total, err := d.usage(path)
		if err != nil {
			b = append(b, fmt.Sprintf("%s: unreadable (%v)", path, err))
			continue
		}
		b = append(b, fmt.Sprintf("%s: %s free of %s (%.0f%%)",
			path, humanBytes(free), humanBytes(total), percent(free, total)))
	}
	sort.Strings(b)
	out := "disk space\n"
	for _, line := range b {
		out += "  " + line + "\n"
	}
	return out
}

func (d *DiskSpace) Check(ctx context.Context) ([]Alert, error) {
	var alerts []Alert
	now := time.Now()
	for _, path := range d.paths {
		if err := ctx.Err(); err != nil {
			return alerts, err
		}
		free, total, err := d.usage(path)
		if err != nil {
			// A path that cannot be read is itself worth knowing about: an
			// unmounted fileserver looks identical to an empty one otherwise.
			if d.ready(path, now) {
				d.last[path] = now
				alerts = append(alerts, Alert{
					Title:   "Disk unreadable",
					Message: fmt.Sprintf("%s could not be checked: %v", path, err),
				})
			}
			continue
		}
		lowPercent := d.minFreePercent > 0 && percent(free, total) < d.minFreePercent
		lowBytes := d.minFreeBytes > 0 && free < d.minFreeBytes
		if !lowPercent && !lowBytes {
			continue
		}
		if !d.ready(path, now) {
			continue
		}
		d.last[path] = now
		alerts = append(alerts, Alert{
			Title: "Disk almost full",
			Message: fmt.Sprintf("%s has %s free of %s (%.1f%%). Recording to a full disk loses the stream.",
				path, humanBytes(free), humanBytes(total), percent(free, total)),
		})
	}
	return alerts, nil
}

// ready reports whether this path may alert again yet. Without a cooldown a
// disk that sits below the threshold sends a message every interval, which
// trains the reader to ignore it.
func (d *DiskSpace) ready(path string, now time.Time) bool {
	if d.cooldown <= 0 {
		return true
	}
	last, seen := d.last[path]
	return !seen || now.Sub(last) >= d.cooldown
}

func percent(free, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(free) / float64(total) * 100
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	case n >= gigabyte:
		return fmt.Sprintf("%.0f GB", float64(n)/gigabyte)
	default:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
}

// statfsUsage reports free and total bytes for the filesystem holding path.
// Bavail rather than Bfree: the blocks reserved for root are not available to
// the recorder, so counting them would report space that cannot be written.
func statfsUsage(path string) (free, total uint64, err error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, err
	}
	size := uint64(fs.Bsize)
	return fs.Bavail * size, fs.Blocks * size, nil
}
