package jobs

import (
	"novapanel/internal/rpcerr"
	"strconv"
	"strings"
	"time"
)

// NextCalendar supports UTC aliases minutely/hourly/daily/weekly/monthly/yearly,
// and [Mon|Tue|Wed|Thu|Fri|Sat|Sun ]*-*-* HH:MM:SS UTC. Time fields
// are decimal or '*'. Ranges, lists, steps, arbitrary dates, timezones, and cron
// expressions are rejected. This is deliberately not full systemd compatibility.
func NextCalendar(expr string, afterMS int64) (int64, error) {
	after := time.UnixMilli(afterMS).UTC()
	start := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
	h, m, s := 0, 0, 0
	day := -1
	monthStart, yearStart := false, false
	switch expr {
	case "minutely":
		h = -1
		m = -1
	case "hourly":
		h = -1
	case "daily":
	case "weekly":
		day = int(time.Monday)
	case "monthly":
		monthStart = true
	case "yearly":
		yearStart = true
	default:
		fields := strings.Fields(expr)
		if len(fields) == 4 {
			days := map[string]int{"Sun": 0, "Mon": 1, "Tue": 2, "Wed": 3, "Thu": 4, "Fri": 5, "Sat": 6}
			var ok bool
			day, ok = days[fields[0]]
			if !ok {
				return 0, badCalendar()
			}
			fields = fields[1:]
		}
		if len(fields) != 3 || fields[0] != "*-*-*" || fields[2] != "UTC" {
			return 0, badCalendar()
		}
		parts := strings.Split(fields[1], ":")
		if len(parts) != 3 {
			return 0, badCalendar()
		}
		values := []*int{&h, &m, &s}
		limits := []int{23, 59, 59}
		for i, p := range parts {
			if p == "*" {
				*values[i] = -1
				continue
			}
			if len(p) != 2 || p[0] < '0' || p[0] > '9' || p[1] < '0' || p[1] > '9' {
				return 0, badCalendar()
			}
			n, e := strconv.Atoi(p)
			if e != nil || n > limits[i] {
				return 0, badCalendar()
			}
			*values[i] = n
		}
	}
	for d := 0; d < 370; d++ {
		date := start.AddDate(0, 0, d)
		if day >= 0 && int(date.Weekday()) != day {
			continue
		}
		if monthStart && date.Day() != 1 {
			continue
		}
		if yearStart && (date.Month() != time.January || date.Day() != 1) {
			continue
		}
		for hh := 0; hh < 24; hh++ {
			if h >= 0 && hh != h {
				continue
			}
			for mm := 0; mm < 60; mm++ {
				if m >= 0 && mm != m {
					continue
				}
				for ss := 0; ss < 60; ss++ {
					if s >= 0 && ss != s {
						continue
					}
					candidate := date.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second).UnixMilli()
					if candidate > afterMS {
						return candidate, nil
					}
				}
			}
		}
	}
	return 0, badCalendar()
}
func badCalendar() error { return rpcerr.Errf("invalid_argument", "unsupported OnCalendar expression") }
