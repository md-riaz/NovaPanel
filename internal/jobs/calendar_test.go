package jobs

import (
	"errors"
	"novapanel/internal/rpcerr"
	"testing"
	"time"
)

func TestCalendarAliasesAndWeekday(t *testing.T) {
	after := time.Date(2026, 10, 30, 12, 30, 0, 500000000, time.UTC).UnixMilli()
	for _, tc := range []struct{ expr, want string }{
		{"minutely", "2026-10-30T12:31:00Z"}, {"hourly", "2026-10-30T13:00:00Z"},
		{"daily", "2026-10-31T00:00:00Z"}, {"weekly", "2026-11-02T00:00:00Z"},
		{"monthly", "2026-11-01T00:00:00Z"}, {"yearly", "2027-01-01T00:00:00Z"},
		{"Mon *-*-* 00:00:00 UTC", "2026-11-02T00:00:00Z"},
		{"*-*-* *:*:* UTC", "2026-10-30T12:30:01Z"},
		{"Fri *-*-* 12:30:00 UTC", "2026-11-06T12:30:00Z"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := NextCalendar(tc.expr, after)
			if err != nil || got != want.UnixMilli() {
				t.Fatalf("got %d %v, want %d", got, err, want.UnixMilli())
			}
		})
	}
}
func TestCalendarRejectsUnsupportedWithInvalid(t *testing.T) {
	for _, expr := range []string{"* * * * *", "tomorrow", "*-*-* 25:00:00 UTC", "*-*-* 12:60:00 UTC", "*-*-* 12:00:60 UTC", "*-*-* 1:00:00 UTC", "*-*-* +1:00:00 UTC", "*-*-* 12:00:00", "*-*-* 12:00:00 Europe/London", "Mon..Fri *-*-* 00:00:00 UTC", "Mon,Tue *-*-* 00:00:00 UTC", "*-*-* */2:00:00 UTC", "2026-10-30 00:00:00 UTC", "daily UTC"} {
		t.Run(expr, func(t *testing.T) {
			_, err := NextCalendar(expr, 0)
			var ce *rpcerr.CodedError
			if !errors.As(err, &ce) || ce.Code != "invalid" {
				t.Fatalf("want invalid, got %v", err)
			}
		})
	}
}
