package app

import "time"

// scheduleLocalMoment is one instant rendered on the wall clock of a named
// IANA zone: the calendar date, weekday and time a person in that zone reads.
// A UTC timestamp alone invites a reader to take the UTC calendar date, which
// differs from the schedule's date for part of every day.
type scheduleLocalMoment struct {
	Zone    string `json:"timezone"`
	Local   string `json:"local_time"`
	Date    string `json:"local_date"`
	Weekday string `json:"local_weekday"`
}

// localMoment renders an RFC3339 instant in zone. An empty zone means UTC. It
// reports false for an unparsable instant or unknown zone, never a guess.
func localMoment(instant, zone string) (scheduleLocalMoment, bool) {
	at, err := time.Parse(time.RFC3339Nano, instant)
	if err != nil {
		return scheduleLocalMoment{}, false
	}
	return localMomentAt(at, zone)
}

func localMomentAt(at time.Time, zone string) (scheduleLocalMoment, bool) {
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return scheduleLocalMoment{}, false
	}
	l := at.In(loc)
	return scheduleLocalMoment{Zone: zone, Local: l.Format("2006-01-02 15:04 MST"), Date: l.Format("2006-01-02"), Weekday: l.Weekday().String()}, true
}

// scheduleOccurrenceOf finds the planned instant and zone of the occurrence a
// run belongs to, walking from the run to its scheduled root.
func (s *Store) scheduleOccurrenceOf(r Run) (scheduledForUTC, zone string, ok bool) {
	id := r.ID
	for i := 0; i <= maxMCPAuthorizationAncestry && id != ""; i++ {
		if s.db.QueryRow(`SELECT scheduled_for_utc,timezone FROM schedule_occurrences WHERE run_id=?`, id).Scan(&scheduledForUTC, &zone) == nil {
			return scheduledForUTC, zone, true
		}
		var parent string
		if s.db.QueryRow(`SELECT COALESCE(parent_run_id,'') FROM runs WHERE id=?`, id).Scan(&parent) != nil {
			return "", "", false
		}
		id = parent
	}
	return "", "", false
}

// scheduleOccurrenceNote tells a scheduled run which local date and weekday the
// occurrence was planned for, in the schedule's own zone.
func (s *Server) scheduleOccurrenceNote(r Run) string {
	planned, zone, ok := s.store.scheduleOccurrenceOf(r)
	if !ok {
		return ""
	}
	m, ok := localMoment(planned, zone)
	if !ok {
		return ""
	}
	return "[scheduled occurrence]\nPlanned for " + m.Local + " " + m.Weekday + " (" + m.Zone + "). The occurrence's local date is " + m.Date + "; use the schedule's zone for every date or weekday this run writes or checks.\n[/scheduled occurrence]"
}
