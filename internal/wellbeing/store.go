package wellbeing

// The days Vita has reported, one row per calendar day.
//
// A day is a JSON object as the app sends it: whatever it knew about that
// day, snake_case keys, numbers in the units their names say (resting_hr in
// bpm, hrv_ms, sleep_h, weight_kg, ...). The server does not keep a schema of
// its own: a field the app learns to send is stored and served the next day
// without a release here.
//
// A report merges into what is stored: a key the report carries replaces the
// stored one, a key it leaves out stays. The app sends the last week every
// time, mostly from its 7–30 day series, and today in full; so an older day
// fills in from the series and keeps what the full report of that day said.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	_ "modernc.org/sqlite"
)

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS days (
		date TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// put merges each day into what is stored for its date.
func (s *store) put(ctx context.Context, days []map[string]any, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, d := range days {
		date, _ := d["date"].(string)
		if !dateRe.MatchString(date) {
			return 0, fmt.Errorf("a day needs date as YYYY-MM-DD, got %q", date)
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return 0, fmt.Errorf("date %q: %w", date, err)
		}
		merged := map[string]any{}
		var raw string
		switch err := tx.QueryRowContext(ctx, `SELECT data FROM days WHERE date = ?`, date).Scan(&raw); {
		case err == nil:
			if err := json.Unmarshal([]byte(raw), &merged); err != nil {
				return 0, fmt.Errorf("stored %s: %w", date, err)
			}
		case errors.Is(err, sql.ErrNoRows):
		default:
			return 0, err
		}
		for k, v := range d {
			if v == nil {
				continue
			}
			merged[k] = v
		}
		stamp := now.UTC().Format(time.RFC3339)
		merged["updated_at"] = stamp
		out, _ := json.Marshal(merged)
		if _, err := tx.ExecContext(ctx, `INSERT INTO days (date, data, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(date) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, date, string(out), stamp); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

// between returns the days from..to inclusive, oldest first.
func (s *store) between(ctx context.Context, from, to string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM days WHERE date >= ? AND date <= ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		d := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// latest is the newest day stored, nil when there is none.
func (s *store) latest(ctx context.Context) (map[string]any, error) {
	var raw string
	switch err := s.db.QueryRowContext(ctx, `SELECT data FROM days ORDER BY date DESC LIMIT 1`).Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	d := map[string]any{}
	return d, json.Unmarshal([]byte(raw), &d)
}

// status is how many days are stored, the span, and when the app last sent.
func (s *store) status(ctx context.Context) (count int, first, last, updated string, err error) {
	var f, l, u sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(date), MAX(date), MAX(updated_at) FROM days`).Scan(&count, &f, &l, &u)
	return count, f.String, l.String, u.String, err
}
