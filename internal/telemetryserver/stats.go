package telemetryserver

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"
)

// Stats is what the dashboard shows.
type Stats struct {
	Installs     int // ever
	Active1      int // in the last day
	Active7      int
	Active30     int
	Days         []Day // the last 30 days, oldest first
	Versions     []Count
	Systems      []Count
	Kinds        []Count
	Langs        []Count
	Syncs        SyncStats
	Errors       []ErrorStat
	MaxDayActive int
}

// Day is one day's active installs and launches.
type Day struct {
	Date     string
	Active   int
	Launches int
}

// Count is how many installs have a value.
type Count struct {
	Value string
	N     int
}

// SyncStats sums the syncs of the last 30 days.
type SyncStats struct {
	Syncs      int
	Files      int64
	Uploaded   int64
	Duplicates int64
	Errors     int64
	Stacks     int64
	Bytes      int64
	Results    []Count
	ErrorKinds []Count
}

// ErrorStat is one error message of the last 7 days.
type ErrorStat struct {
	Message  string
	Count    int
	Installs int
	Versions string
}

// Stats computes the dashboard's numbers as of now.
func (s *Store) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	day := func(n int) int64 { return now.Add(-time.Duration(n) * 24 * time.Hour).Unix() }

	row := s.db.QueryRowContext(ctx, `SELECT
		COUNT(DISTINCT install),
		COUNT(DISTINCT CASE WHEN at >= ? THEN install END),
		COUNT(DISTINCT CASE WHEN at >= ? THEN install END),
		COUNT(DISTINCT CASE WHEN at >= ? THEN install END)
		FROM events`, day(1), day(7), day(30))
	if err := row.Scan(&st.Installs, &st.Active1, &st.Active7, &st.Active30); err != nil {
		return st, err
	}

	// Every one of the last 30 days, including those without events.
	byDate := map[string]Day{}
	rows, err := s.db.QueryContext(ctx, `SELECT date(at, 'unixepoch'), COUNT(DISTINCT install),
		SUM(type = 'launch') FROM events WHERE at >= ? GROUP BY 1`, day(30))
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.Date, &d.Active, &d.Launches); err != nil {
			rows.Close()
			return st, err
		}
		byDate[d.Date] = d
	}
	rows.Close()
	for i := 29; i >= 0; i-- {
		date := now.UTC().AddDate(0, 0, -i).Format("2006-01-02")
		d, ok := byDate[date]
		if !ok {
			d = Day{Date: date}
		}
		st.Days = append(st.Days, d)
		st.MaxDayActive = max(st.MaxDayActive, d.Active)
	}

	// Each install counted once, with what its latest event says.
	versions, systems, kinds, langs := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	rows, err = s.db.QueryContext(ctx, `SELECT version, os, os_version, kind, lang, MAX(id)
		FROM events WHERE at >= ? GROUP BY install`, day(30))
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var version, system, osVersion, kind, lang string
		var id int64
		if err := rows.Scan(&version, &system, &osVersion, &kind, &lang, &id); err != nil {
			rows.Close()
			return st, err
		}
		versions[version]++
		if osVersion != "" {
			system += " " + osVersion
		}
		systems[system]++
		kinds[kind]++
		langs[cmp.Or(lang, "?")]++
	}
	rows.Close()
	st.Versions, st.Systems, st.Kinds, st.Langs = sorted(versions), sorted(systems), sorted(kinds), sorted(langs)

	if st.Syncs, err = s.syncStats(ctx, day(30)); err != nil {
		return st, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT json_extract(data, '$.message'), COUNT(*),
		COUNT(DISTINCT install), GROUP_CONCAT(DISTINCT version)
		FROM events WHERE type = 'error' AND at >= ? GROUP BY 1 ORDER BY 2 DESC LIMIT 30`, day(7))
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ErrorStat
		if err := rows.Scan(&e.Message, &e.Count, &e.Installs, &e.Versions); err != nil {
			return st, err
		}
		st.Errors = append(st.Errors, e)
	}
	return st, rows.Err()
}

func (s *Store) syncStats(ctx context.Context, since int64) (SyncStats, error) {
	var st SyncStats
	results, errorKinds := map[string]int{}, map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM events WHERE type = 'sync' AND at >= ?`, since)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return st, err
		}
		var d struct {
			Result     string           `json:"result"`
			Files      int64            `json:"files"`
			Uploaded   int64            `json:"uploaded"`
			Duplicates int64            `json:"duplicates"`
			Errors     int64            `json:"errors"`
			Stacks     int64            `json:"stacks"`
			Bytes      int64            `json:"bytes"`
			ErrorKinds map[string]int64 `json:"errorKinds"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue
		}
		st.Syncs++
		st.Files += d.Files
		st.Uploaded += d.Uploaded
		st.Duplicates += d.Duplicates
		st.Errors += d.Errors
		st.Stacks += d.Stacks
		st.Bytes += d.Bytes
		results[cmp.Or(d.Result, "?")]++
		for k, n := range d.ErrorKinds {
			errorKinds[k] += int(n)
		}
	}
	st.Results, st.ErrorKinds = sorted(results), sorted(errorKinds)
	return st, rows.Err()
}

// sorted lists m's counts, largest first.
func sorted(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for v, n := range m {
		out = append(out, Count{Value: v, N: n})
	}
	slices.SortFunc(out, func(a, b Count) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.Value, b.Value))
	})
	return out
}
