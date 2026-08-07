package plugin

import "testing"

func TestParseSeverity(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Severity
		wantErr bool
	}{
		{"info", SeverityInfo, false},
		{"warning", SeverityWarning, false},
		{"critical", SeverityCritical, false},
		// Saying nothing must not mean silence: an unheard alert is worse than
		// a needless buzz, so the default notifies.
		{"", SeverityWarning, false},
		{"quiet", "", true},
		{"INFO", "", true},
	} {
		got, err := ParseSeverity(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseSeverity(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSeverity(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseSeverity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every alert must carry a severity, or it inherits the zero value and the
// notifier has to guess.
func TestDiskSpaceAlertsCarrySeverity(t *testing.T) {
	d := NewDiskSpace([]string{"/data"}, 10, 0, 0)
	d.usage = func(string) (uint64, uint64, error) {
		return uint64(1 * gigabyte), uint64(1000 * gigabyte), nil
	}
	alerts, err := d.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if alerts[0].Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", alerts[0].Severity)
	}
}
