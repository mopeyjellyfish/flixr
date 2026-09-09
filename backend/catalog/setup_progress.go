package catalog

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	SetupProgressChoice    = "choice"
	SetupProgressLibraries = "libraries"
	SetupProgressProfile   = "profile"
	SetupProgressComplete  = "complete"
)

func validSetupProgress(step string) bool {
	switch step {
	case SetupProgressChoice, SetupProgressLibraries, SetupProgressProfile, SetupProgressComplete:
		return true
	default:
		return false
	}
}

// SetupProgress returns the last owner-confirmed setup step. An empty value is
// intentional for installations created before resumable setup was introduced.
func (c *Catalog) SetupProgress() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.db == nil {
		return c.setupProgress, nil
	}
	var step string
	err := c.db.QueryRow(`SELECT value FROM settings WHERE key='setup_progress'`).Scan(&step)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load setup progress: %w", err)
	}
	if !validSetupProgress(step) {
		return "", errors.New("invalid persisted setup progress")
	}
	return step, nil
}

// SetSetupProgress stores only known non-secret state labels.
func (c *Catalog) SetSetupProgress(step string) error {
	if !validSetupProgress(step) {
		return errors.New("invalid setup progress")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		if _, err := c.db.Exec(`INSERT INTO settings(key,value) VALUES('setup_progress',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, step); err != nil {
			return fmt.Errorf("save setup progress: %w", err)
		}
	}
	c.setupProgress = step
	return nil
}
