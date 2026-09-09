package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	massRemovalMinimum = 10
	massRemovalPercent = 50
)

type rootScan struct {
	locationID, kind, path string
	items                  int
	missing                int
}

type locationScanError struct {
	scanID string
	root   rootScan
	cause  error
}

func (e *locationScanError) Error() string { return e.cause.Error() }
func (e *locationScanError) Unwrap() error { return e.cause }

type activeSource struct {
	physicalID, catalogID, relativePath string
}

var ErrRemovalReviewNotFound = errors.New("library removal review not found")

type LibraryLocation struct {
	ID                  string `json:"id"`
	LibraryID           string `json:"library_id"`
	LibraryName         string `json:"library_name,omitempty"`
	RootPath            string `json:"path,omitempty"`
	Revision            int64  `json:"revision,omitempty"`
	RootKind            string `json:"root_kind"`
	State               string `json:"state"`
	ScanComplete        bool   `json:"scan_complete"`
	Items               int    `json:"items"`
	Missing             int    `json:"missing"`
	PendingScanID       string `json:"pending_scan_id,omitempty"`
	LastScanID          string `json:"last_scan_id,omitempty"`
	UpdatedAt           int64  `json:"updated_at"`
	Message             string `json:"message,omitempty"`
	PendingChangeID     string `json:"pending_change_id,omitempty"`
	PendingRootPath     string `json:"pending_path,omitempty"`
	PendingChangeOrigin string `json:"pending_change_origin,omitempty"`
}

func (c *Catalog) LibraryLocations() ([]LibraryLocation, error) {
	if c.db == nil {
		return []LibraryLocation{}, nil
	}
	rows, err := c.db.Query(`SELECT x.id,x.library_id,l.name,l.kind,x.state,x.scan_complete,x.item_count,x.missing_count,x.pending_scan_id,x.last_scan_id,x.updated_at,x.message FROM library_locations x JOIN libraries l ON l.id=x.library_id ORDER BY l.created_at,l.id,x.id`)
	if err != nil {
		return nil, fmt.Errorf("load library locations: %w", err)
	}
	defer rows.Close()
	locations := make([]LibraryLocation, 0, 2)
	for rows.Next() {
		var location LibraryLocation
		var complete int
		if err := rows.Scan(&location.ID, &location.LibraryID, &location.LibraryName, &location.RootKind, &location.State, &complete, &location.Items, &location.Missing, &location.PendingScanID, &location.LastScanID, &location.UpdatedAt, &location.Message); err != nil {
			return nil, fmt.Errorf("read library location: %w", err)
		}
		location.ScanComplete = complete != 0
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read library locations: %w", err)
	}
	return locations, nil
}

func (c *Catalog) ConfirmRemovals(ctx context.Context, scanID, locationID string) error {
	if scanID == "" || locationID == "" || c.db == nil {
		return ErrRemovalReviewNotFound
	}
	if locationID == "film" {
		locationID = "films-root"
	} else if locationID == "episode" {
		locationID = "tv-root"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return ErrScanActive
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin library cleanup: %w", err)
	}
	defer tx.Rollback()
	var pending, state string
	if err := tx.QueryRowContext(ctx, `SELECT pending_scan_id,state FROM library_locations WHERE id=?`, locationID).Scan(&pending, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRemovalReviewNotFound
		}
		return fmt.Errorf("load library cleanup review: %w", err)
	}
	if state != "review_required" || pending != scanID {
		return ErrRemovalReviewNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT physical_file_id,catalog_id FROM library_removal_candidates WHERE location_id=? AND scan_id=? ORDER BY physical_file_id`, locationID, scanID)
	if err != nil {
		return fmt.Errorf("load library cleanup candidates: %w", err)
	}
	catalogIDs := map[string]bool{}
	for rows.Next() {
		var physicalID, catalogID string
		if err := rows.Scan(&physicalID, &catalogID); err != nil {
			rows.Close()
			return fmt.Errorf("read library cleanup candidate: %w", err)
		}
		catalogIDs[catalogID] = true
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_physical_files SET present=0,selected=0 WHERE id=? AND location_id=?`, physicalID, locationID); err != nil {
			rows.Close()
			return fmt.Errorf("confirm physical source removal: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read library cleanup candidates: %w", err)
	}
	rows.Close()
	if len(catalogIDs) == 0 {
		return ErrRemovalReviewNotFound
	}
	switched := make(map[string]bool, len(catalogIDs))
	ids := make([]string, 0, len(catalogIDs))
	for catalogID := range catalogIDs {
		changed, err := reselectCatalogSource(ctx, tx, catalogID)
		if err != nil {
			return fmt.Errorf("select remaining catalog source: %w", err)
		}
		switched[catalogID] = changed
		ids = append(ids, catalogID)
	}
	if err := applyIdentityMappings(tx); err != nil {
		return fmt.Errorf("apply cleanup availability: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_removal_candidates WHERE location_id=? AND scan_id=?`, locationID, scanID); err != nil {
		return fmt.Errorf("clear library cleanup review: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_locations SET state='available',scan_complete=1,missing_count=0,pending_scan_id='',updated_at=?,message='' WHERE id=?`, time.Now().Unix(), locationID); err != nil {
		return fmt.Errorf("complete library cleanup review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit library cleanup: %w", err)
	}
	if err := c.refreshReselectedItems(ids, switched); err != nil {
		return fmt.Errorf("refresh cleaned catalog source: %w", err)
	}
	c.refreshSeriesAvailability()
	if c.status.ID == scanID && c.status.Status == "review_required" {
		c.status.Status = "complete"
		c.status.Message = "owner confirmed library cleanup"
		c.saveStatus(c.status)
	}
	return nil
}

func (c *Catalog) activeSources(locationID string, previous map[scanKey]Item) ([]activeSource, error) {
	if c.db == nil {
		out := make([]activeSource, 0)
		for key, item := range previous {
			if key.locationID == locationID && item.Playable {
				out = append(out, activeSource{catalogID: item.ID, relativePath: key.rel})
			}
		}
		return out, nil
	}
	rows, err := c.db.Query(`SELECT id,catalog_id,relative_path FROM catalog_physical_files WHERE location_id=? AND present=1 ORDER BY relative_path,id`, locationID)
	if err != nil {
		return nil, fmt.Errorf("load active library sources: %w", err)
	}
	defer rows.Close()
	out := make([]activeSource, 0)
	for rows.Next() {
		var source activeSource
		if err := rows.Scan(&source.physicalID, &source.catalogID, &source.relativePath); err != nil {
			return nil, fmt.Errorf("read active library source: %w", err)
		}
		out = append(out, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read active library sources: %w", err)
	}
	return out, nil
}

func suspiciousRemoval(previous, missing, remaining int) bool {
	if missing == 0 {
		return false
	}
	if remaining == 0 {
		return true
	}
	return missing >= massRemovalMinimum && missing*100 >= previous*massRemovalPercent
}

func (c *Catalog) recordRemovalReview(scanID string, root rootScan, sources []activeSource) error {
	if c.db == nil {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin library removal review: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM library_removal_candidates WHERE location_id=?`, root.locationID); err != nil {
		return fmt.Errorf("reset library removal review: %w", err)
	}
	for _, source := range sources {
		if _, err := tx.Exec(`INSERT INTO library_removal_candidates(location_id,scan_id,physical_file_id,catalog_id) VALUES(?,?,?,?)`, root.locationID, scanID, source.physicalID, source.catalogID); err != nil {
			return fmt.Errorf("save library removal candidate: %w", err)
		}
	}
	message := fmt.Sprintf("Review %d missing files before confirming cleanup.", len(sources))
	if _, err := tx.Exec(`UPDATE library_locations SET root_path=?,state='review_required',scan_complete=0,item_count=?,missing_count=?,pending_scan_id=?,last_scan_id=?,updated_at=?,message=? WHERE id=?`, root.path, root.items, len(sources), scanID, scanID, time.Now().Unix(), message, root.locationID); err != nil {
		return fmt.Errorf("save library removal review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit library removal review: %w", err)
	}
	return nil
}

func (c *Catalog) recordUnavailableLocation(scanID string, root rootScan, cause error) {
	if c.db == nil {
		return
	}
	message := cause.Error()
	_, _ = c.db.Exec(`UPDATE library_locations SET state='unavailable',scan_complete=0,pending_scan_id='',last_scan_id=?,updated_at=?,message=? WHERE id=?`, scanID, time.Now().Unix(), message, root.locationID)
	_, _ = c.db.Exec(`DELETE FROM library_removal_candidates WHERE location_id=?`, root.locationID)
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func recordCompleteLocationsTx(tx sqlExecutor, scanID string, roots []rootScan) error {
	for _, root := range roots {
		if _, err := tx.Exec(`DELETE FROM library_removal_candidates WHERE location_id=?`, root.locationID); err != nil {
			return fmt.Errorf("clear library removal review: %w", err)
		}
		if _, err := tx.Exec(`UPDATE library_locations SET root_path=?,state='available',scan_complete=1,item_count=?,missing_count=?,pending_scan_id='',last_scan_id=?,updated_at=?,message='' WHERE id=?`, root.path, root.items, root.missing, scanID, time.Now().Unix(), root.locationID); err != nil {
			return fmt.Errorf("save library location: %w", err)
		}
	}
	return nil
}
