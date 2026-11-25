package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Names the person gave their conversations. They live beside the agent's
// database, not in it: the agent names a session after its first question and
// may rewrite that, and a name the person chose is not the agent's to lose.
var titlesMu sync.Mutex

func titlesPath(dataDir string) string { return filepath.Join(dataDir, "session-titles.json") }

func readTitles(dataDir string) map[string]string {
	out := map[string]string{}
	if dataDir == "" {
		return out
	}
	if b, err := os.ReadFile(titlesPath(dataDir)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// RenameSession names a conversation. An empty name takes the given one away,
// and the conversation is called after its opening question again.
func (s *Service) RenameSession(id, title string) error {
	id = strings.TrimSpace(id)
	if s == nil || id == "" {
		return errors.New("no session id")
	}
	title = strings.Join(strings.Fields(title), " ")
	if r := []rune(title); len(r) > 120 {
		title = string(r[:120])
	}
	titlesMu.Lock()
	defer titlesMu.Unlock()
	all := readTitles(s.dataDir)
	if title == "" {
		delete(all, id)
	} else {
		all[id] = title
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return os.WriteFile(titlesPath(s.dataDir), b, 0o600)
}

func (s *Service) forgetTitle(id string) {
	if s == nil {
		return
	}
	titlesMu.Lock()
	defer titlesMu.Unlock()
	all := readTitles(s.dataDir)
	if _, ok := all[id]; ok {
		delete(all, id)
		if b, err := json.Marshal(all); err == nil {
			_ = os.WriteFile(titlesPath(s.dataDir), b, 0o600)
		}
	}
}
