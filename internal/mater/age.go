package mater

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseAge accepts 45m, 6h, 2d, 1w, or a bare number read as days, so
// `--stale 5` and `--stale 5d` agree. A typo fails rather than silently
// becoming zero.
func ParseAge(s string) (time.Duration, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return 0, fmt.Errorf("empty age")
	}

	unit := v[len(v)-1]
	num := v
	switch unit {
	case 's', 'm', 'h', 'd', 'w':
		num = v[:len(v)-1]
	default:
		unit = 'd'
	}

	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad age %q: expected 45m, 6h, 2d, 1w, or a number of days", s)
	}

	switch unit {
	case 's':
		return time.Duration(n) * time.Second, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return time.Duration(n) * 24 * time.Hour, nil
	}
}

// FormatAge renders a duration at a single unit of resolution.
func FormatAge(d time.Duration) string {
	switch {
	case d < 0:
		return "—"
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int64(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	default:
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
}

// FormatSize renders bytes of disk usage in the largest unit that keeps the
// number readable.
func FormatSize(b int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0fM", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.0fK", float64(b)/kb)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// Plural returns the correct suffix for n of a regular noun.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
