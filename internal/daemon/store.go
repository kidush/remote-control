package daemon

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/kidush/remote-control/internal/ac"
)

// Store keeps the last confirmed state of every device on disk so it
// survives restarts.
type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) Load() (map[string]ac.State, error) {
	out := map[string]ac.State{}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(b, &out)
}

func (s *Store) Save(states map[string]ac.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
