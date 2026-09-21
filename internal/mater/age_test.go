package mater

import (
	"testing"
	"time"
)

func TestParseAge(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"45m", 45 * time.Minute},
		{"6h", 6 * time.Hour},
		{"2d", 48 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"30s", 30 * time.Second},
		{"5", 5 * 24 * time.Hour},  // a bare number means days
		{"5D", 5 * 24 * time.Hour}, // case insensitive
		{"0", 0},
		{" 3d ", 3 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := ParseAge(c.in)
		if err != nil {
			t.Errorf("ParseAge(%q) returned %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAge(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// A typo has to fail loudly: silently becoming zero would turn "--stale 5x"
// into "take everything".
func TestParseAgeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "x", "5x", "-3d", "d", "1.5d", "two days"} {
		if got, err := ParseAge(in); err == nil {
			t.Errorf("ParseAge(%q) = %v, want an error", in, got)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1K"},
		{5 * 1024 * 1024, "5M"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
		{1536 * 1024 * 1024, "1.5G"},
	}
	for _, c := range cases {
		if got := FormatSize(c.in); got != c.want {
			t.Errorf("FormatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-1, "—"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{90 * time.Minute, "1h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := FormatAge(c.in); got != c.want {
			t.Errorf("FormatAge(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
