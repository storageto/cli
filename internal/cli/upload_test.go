package cli

import (
	"strings"
	"testing"
)

func TestParseExpire(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr string
	}{
		{"", 0, ""},
		{"1d", 1, ""},
		{"7d", 7, ""},
		{"3", 3, ""},
		{" 2D ", 2, ""},
		{"0d", 0, "between 1d and 7d"},
		{"8d", 0, "between 1d and 7d"},
		{"-1", 0, "between 1d and 7d"},
		{"24h", 0, "hours are not supported"},
		{"soon", 0, "number of days"},
		{"1.5d", 0, "number of days"},
	}
	for _, c := range cases {
		got, err := parseExpire(c.in)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("parseExpire(%q) error = %v", c.in, err)
			} else if got != c.want {
				t.Errorf("parseExpire(%q) = %d, want %d", c.in, got, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("parseExpire(%q) error = %v, want containing %q", c.in, err, c.wantErr)
		}
	}
}

func TestUploadOptions(t *testing.T) {
	opts, err := uploadOptions("", false, 0)
	if err != nil || opts.ExpiryDays != 0 || opts.MaxDownloads != 0 {
		t.Fatalf("defaults: got %+v, %v; want zero options", opts, err)
	}

	opts, err = uploadOptions("2d", true, 0)
	if err != nil || opts.ExpiryDays != 2 || opts.MaxDownloads != 1 {
		t.Fatalf("--expire 2d --burn-after: got %+v, %v", opts, err)
	}

	// --burn-after with an explicit --max-downloads 1 is redundant, not wrong.
	if _, err := uploadOptions("", true, 1); err != nil {
		t.Errorf("--burn-after --max-downloads 1: unexpected error %v", err)
	}
	if _, err := uploadOptions("", true, 5); err == nil {
		t.Error("--burn-after --max-downloads 5: want a conflict error")
	}
	if _, err := uploadOptions("", false, 1001); err == nil {
		t.Error("--max-downloads 1001: want a range error")
	}
	if _, err := uploadOptions("", false, -1); err == nil {
		t.Error("--max-downloads -1: want a range error")
	}
	// A bad --expire is reported even when the download flags are fine.
	if _, err := uploadOptions("9d", false, 0); err == nil {
		t.Error("--expire 9d: want a range error")
	}
}
