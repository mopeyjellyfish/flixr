package catalog

// SetTMDBToken persists the owner credential but never exposes it through catalog views.
func (c *Catalog) SetTMDBToken(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
	if c.db == nil {
		return nil
	}
	if token == "" {
		_, err := c.db.Exec("DELETE FROM settings WHERE key='tmdb_token'")
		return err
	}
	_, err := c.db.Exec("INSERT INTO settings(key,value) VALUES('tmdb_token',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", token)
	return err
}
func (c *Catalog) TMDBConfigured() bool { c.mu.RLock(); defer c.mu.RUnlock(); return c.token != "" }

// SetProvider makes the scan-time provider replaceable at its narrow consumer-owned seam.
func (c *Catalog) SetProvider(provider MetadataProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = provider
}
