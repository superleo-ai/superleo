package backend

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// The life-assistant writes, as methods rather than tool closures.
//
// Two callers need them now: the agent, through add_schedule, and a button the
// model drew in a `ui` block, through the App method of the same name. They must agree on the stored shape — a row written by a click that
// list_schedules then reads back differently is a bug that only shows up later,
// in the model's own summary of the day.
//
// The validation lives here for the same reason. It used to be only in the tool
// schemas, which the agent respects and a UI action never sees.

// AddSchedule appends one calendar entry and returns the row as stored.
func (s *Service) AddSchedule(title, startAt, location string, participants []string) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("life store is unavailable")
	}
	title = strings.TrimSpace(title)
	startAt = strings.TrimSpace(startAt)
	if title == "" {
		return nil, errors.New("title is required")
	}
	if startAt == "" {
		return nil, errors.New("start_at is required")
	}
	rec := map[string]any{
		"id":           short(uuid.NewString()),
		"title":        title,
		"start_at":     startAt,
		"location":     strings.TrimSpace(location),
		"participants": trimmed(participants),
	}
	s.store.mu.Lock()
	s.store.Schedules = append(s.store.Schedules, rec)
	s.store.mu.Unlock()
	s.store.save()
	return rec, nil
}

// Forget removes one calendar entry the person says is wrong, by id. It is the
// only delete there is; the agent's tools only ever add, so a mistake it wrote
// stays until someone removes it. Notes and people live in noted, which has its
// own deletes.
func (s *Service) Forget(kind, key string) error {
	if s == nil || s.store == nil {
		return errors.New("life store is unavailable")
	}
	key = strings.TrimSpace(key)
	drop := func(rows []map[string]any) ([]map[string]any, bool) {
		for i, r := range rows {
			if id, _ := r["id"].(string); id == key {
				return append(rows[:i:i], rows[i+1:]...), true
			}
		}
		return rows, false
	}
	s.store.mu.Lock()
	found := false
	switch kind {
	case "schedule":
		s.store.Schedules, found = drop(s.store.Schedules)
	default:
		s.store.mu.Unlock()
		return errors.New("nothing of kind " + kind + " can be forgotten")
	}
	s.store.mu.Unlock()
	if !found {
		return errors.New("no such " + kind + ": " + key)
	}
	s.store.save()
	return nil
}

// trimmed drops blank entries and surrounding space, and never returns nil:
// the rows are serialized to JSON, and a nil slice becomes `null` where every
// reader expects a list.
func trimmed(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
