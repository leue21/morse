package plugin

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeDisk builds a plugin whose filesystem reports fixed figures.
func fakeDisk(t *testing.T, freeGB, totalGB float64, minPercent, minGB float64, cooldown time.Duration) *DiskSpace {
	t.Helper()
	d := NewDiskSpace([]string{"/data"}, minPercent, minGB, cooldown)
	d.usage = func(string) (uint64, uint64, error) {
		return uint64(freeGB * gigabyte), uint64(totalGB * gigabyte), nil
	}
	return d
}

func TestDiskSpaceAlertsOnEitherThreshold(t *testing.T) {
	ctx := context.Background()

	// Comfortable on both counts.
	quiet := fakeDisk(t, 500, 1000, 10, 100, 0)
	if alerts, err := quiet.Check(ctx); err != nil || len(alerts) != 0 {
		t.Fatalf("healthy disk alerted: %+v (err %v)", alerts, err)
	}

	// 5% free, but 50 GB — the percentage is what catches this.
	byPercent := fakeDisk(t, 50, 1000, 10, 10, 0)
	alerts, err := byPercent.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("low percentage did not alert: %+v", alerts)
	}
	if !strings.Contains(alerts[0].Message, "/data") {
		t.Errorf("alert does not name the path: %q", alerts[0].Message)
	}

	// 40% free, but only 8 GB — on a small disk the absolute floor is what
	// catches it, and a percentage alone would say everything is fine.
	byBytes := fakeDisk(t, 8, 20, 10, 20, 0)
	if alerts, _ := byBytes.Check(ctx); len(alerts) != 1 {
		t.Errorf("low absolute space did not alert: %+v", alerts)
	}
}

// A disk that stays below the threshold must not alert on every interval, or
// the alerts become noise and stop being read.
func TestDiskSpaceCooldownSuppressesRepeats(t *testing.T) {
	ctx := context.Background()
	d := fakeDisk(t, 5, 1000, 10, 0, time.Hour)

	if alerts, _ := d.Check(ctx); len(alerts) != 1 {
		t.Fatalf("first check should alert: %+v", alerts)
	}
	if alerts, _ := d.Check(ctx); len(alerts) != 0 {
		t.Errorf("second check inside the cooldown alerted again: %+v", alerts)
	}
	// Once the cooldown has passed it speaks up again: the disk is still full.
	d.last["/data"] = time.Now().Add(-2 * time.Hour)
	if alerts, _ := d.Check(ctx); len(alerts) != 1 {
		t.Errorf("cooldown expired but no alert: %+v", alerts)
	}
}

// An unmounted fileserver reads as an error, not as an empty disk. Staying
// silent would hide exactly the case worth knowing about.
func TestDiskSpaceReportsUnreadablePaths(t *testing.T) {
	d := NewDiskSpace([]string{"/mnt/gone"}, 10, 0, 0)
	d.usage = func(string) (uint64, uint64, error) {
		return 0, 0, errStat
	}
	alerts, err := d.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0].Title, "unreadable") {
		t.Fatalf("unreadable path did not alert: %+v", alerts)
	}
}

func TestDiskSpaceDescribeShowsEveryPath(t *testing.T) {
	d := fakeDisk(t, 250, 1000, 10, 0, 0)
	got := d.Describe()
	if !strings.Contains(got, "/data") || !strings.Contains(got, "250 GB") {
		t.Errorf("Describe() = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   uint64
		want string
	}{
		{500 * (1 << 20), "500 MB"},
		{8 * gigabyte, "8 GB"},
		{2 * (1 << 40), "2.0 TB"},
	} {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

var errStat = statError("no such device")

type statError string

func (e statError) Error() string { return string(e) }
