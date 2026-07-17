package update

import (
	"testing"
	"time"
)

var (
	now      = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	twoDays  = now.Add(-48 * time.Hour)
	oneHour  = now.Add(-1 * time.Hour)
	tenHours = now.Add(-10 * time.Hour)
)

func TestNoticeDue(t *testing.T) {
	base := State{
		CheckedAt:   oneHour,
		LatestTag:   "v0.2.0",
		PublishedAt: twoDays,
	}
	cases := []struct {
		name    string
		mutate  func(*State)
		current string
		want    bool
	}{
		{"newer release announced", func(st *State) {}, "0.1.0", true},
		{"up to date", func(st *State) {}, "0.2.0", false},
		{"dev build silent", func(st *State) {}, "0.0.0-dev", false},
		{"release younger than 24h grace", func(st *State) { st.PublishedAt = tenHours }, "0.1.0", false},
		{"noticed recently", func(st *State) { st.NoticedAt = tenHours }, "0.1.0", false},
		{"notice throttle expired", func(st *State) { st.NoticedAt = twoDays }, "0.1.0", true},
		{"no check result yet", func(st *State) { st.LatestTag = "" }, "0.1.0", false},
		{"unknown publish time still announced", func(st *State) { st.PublishedAt = time.Time{} }, "0.1.0", true},
	}
	for _, c := range cases {
		st := base
		c.mutate(&st)
		if got := NoticeDue(st, c.current, now); got != c.want {
			t.Errorf("%s: NoticeDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckDue(t *testing.T) {
	cases := []struct {
		name    string
		st      State
		current string
		env     string
		want    bool
	}{
		{"never checked", State{}, "0.1.0", "", true},
		{"checked an hour ago", State{CheckedAt: oneHour}, "0.1.0", "", false},
		{"checked two days ago", State{CheckedAt: twoDays}, "0.1.0", "", true},
		{"dev build never checks", State{}, "0.0.0-dev", "", false},
		{"opt-out env", State{}, "0.1.0", "1", false},
	}
	for _, c := range cases {
		t.Setenv("SANDCASTLE_NO_UPDATE_NOTIFIER", c.env)
		if got := CheckDue(c.st, c.current, now); got != c.want {
			t.Errorf("%s: CheckDue = %v, want %v", c.name, got, c.want)
		}
	}
}
