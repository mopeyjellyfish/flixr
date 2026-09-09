package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrLibraryNotFound              = errors.New("library not found")
	ErrLocationNotFound             = errors.New("library location not found")
	ErrOverlappingLocation          = errors.New("library locations overlap")
	ErrLocationChangeReviewRequired = errors.New("populated library location change requires preview")
	ErrInvalidLibrary               = errors.New("invalid library")
	ErrLibraryHasLocations          = errors.New("library still has locations")
)

type Library struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Locations []LibraryLocation `json:"locations"`
}

type scanRoot struct {
	id, libraryID, libraryName, path, kind string
}

type LocationChangePreview struct {
	ID              string `json:"id"`
	LocationID      string `json:"location_id"`
	NewRootPath     string `json:"new_path,omitempty"`
	AffectedSources int    `json:"affected_sources"`
	AffectedTitles  int    `json:"affected_titles"`
}

func (c *Catalog) normalizeLocationTopology() error {
	if c.db == nil {
		return nil
	}
	rows, err := c.db.Query(`SELECT x.id,x.root_path,x.state FROM library_locations x JOIN libraries l ON l.id=x.library_id ORDER BY l.created_at,l.id,x.id`)
	if err != nil {
		return err
	}
	type location struct{ id, path, state, canonical string }
	var locations []location
	for rows.Next() {
		var x location
		if err := rows.Scan(&x.id, &x.path, &x.state); err != nil {
			rows.Close()
			return err
		}
		x.canonical = filepath.Clean(x.path)
		if resolved, resolveErr := filepath.EvalSymlinks(x.canonical); resolveErr == nil {
			x.canonical = filepath.Clean(resolved)
		}
		locations = append(locations, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active []location
	for _, x := range locations {
		conflict := ""
		for _, prior := range active {
			if pathsOverlap(x.canonical, prior.canonical) {
				conflict = prior.id
				break
			}
		}
		if conflict != "" {
			if _, err := tx.Exec(`UPDATE library_locations SET state='topology_review',scan_complete=0,message=? WHERE id=?`, "Folder overlaps "+conflict+"; move or remove it before scanning.", x.id); err != nil {
				return err
			}
			continue
		}
		active = append(active, x)
		if x.state == "topology_review" {
			if _, err := tx.Exec(`UPDATE library_locations SET state='unknown',message='' WHERE id=?`, x.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (c *Catalog) Libraries() ([]Library, error) {
	if c.db == nil {
		libraries := []Library{{ID: "films", Name: "Films", Kind: "film", Locations: []LibraryLocation{}}, {ID: "tv", Name: "TV", Kind: "episode", Locations: []LibraryLocation{}}}
		if c.film != "" {
			libraries[0].Locations = append(libraries[0].Locations, LibraryLocation{ID: "films-root", LibraryID: "films", RootPath: c.film})
		}
		if c.tv != "" {
			libraries[1].Locations = append(libraries[1].Locations, LibraryLocation{ID: "tv-root", LibraryID: "tv", RootPath: c.tv})
		}
		return libraries, nil
	}
	rows, err := c.db.Query(`SELECT l.id,l.name,l.kind,x.id,x.root_path,x.revision,x.state,x.scan_complete,x.item_count,x.missing_count,x.pending_scan_id,x.last_scan_id,x.updated_at,x.message,p.id,p.new_root_path,p.origin FROM libraries l LEFT JOIN library_locations x ON x.library_id=l.id LEFT JOIN library_location_removal_previews p ON p.location_id=x.id ORDER BY l.created_at,l.name COLLATE NOCASE,l.id,x.id`)
	if err != nil {
		return nil, fmt.Errorf("load libraries: %w", err)
	}
	defer rows.Close()
	var libraries []Library
	byID := map[string]int{}
	for rows.Next() {
		var library Library
		var locationID, rootPath, state, pending, last, message, pendingChangeID, pendingRootPath, pendingChangeOrigin sql.NullString
		var revision, complete, items, missing, updated sql.NullInt64
		if err := rows.Scan(&library.ID, &library.Name, &library.Kind, &locationID, &rootPath, &revision, &state, &complete, &items, &missing, &pending, &last, &updated, &message, &pendingChangeID, &pendingRootPath, &pendingChangeOrigin); err != nil {
			return nil, fmt.Errorf("read libraries: %w", err)
		}
		index, ok := byID[library.ID]
		if !ok {
			library.Locations = []LibraryLocation{}
			byID[library.ID] = len(libraries)
			libraries = append(libraries, library)
			index = len(libraries) - 1
		}
		if locationID.Valid {
			libraries[index].Locations = append(libraries[index].Locations, LibraryLocation{ID: locationID.String, LibraryID: library.ID, LibraryName: library.Name, RootPath: rootPath.String, Revision: revision.Int64, State: state.String, ScanComplete: complete.Int64 != 0, Items: int(items.Int64), Missing: int(missing.Int64), PendingScanID: pending.String, LastScanID: last.String, UpdatedAt: updated.Int64, Message: message.String, PendingChangeID: pendingChangeID.String, PendingRootPath: pendingRootPath.String, PendingChangeOrigin: pendingChangeOrigin.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read libraries: %w", err)
	}
	return libraries, nil
}

func (c *Catalog) CreateLibrary(name, kind string) (Library, error) {
	name = strings.TrimSpace(name)
	if name == "" || (kind != "film" && kind != "episode") || c.db == nil {
		return Library{}, ErrInvalidLibrary
	}
	id, err := randomScanID()
	if err != nil {
		return Library{}, fmt.Errorf("generate library ID: %w", err)
	}
	tx, err := c.db.Begin()
	if err != nil {
		return Library{}, fmt.Errorf("begin library creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO libraries(id,name,kind,created_at) VALUES(?,?,?,?)`, id, name, kind, time.Now().UnixMilli()); err != nil {
		return Library{}, fmt.Errorf("create library: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO library_scan_policies(library_id) VALUES(?)`, id); err != nil {
		return Library{}, fmt.Errorf("create library scan policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Library{}, fmt.Errorf("commit library creation: %w", err)
	}
	return Library{ID: id, Name: name, Kind: kind, Locations: []LibraryLocation{}}, nil
}

func (c *Catalog) RenameLibrary(id, name string) (Library, error) {
	name = strings.TrimSpace(name)
	if id == "" || name == "" || c.db == nil {
		return Library{}, ErrInvalidLibrary
	}
	result, err := c.db.Exec(`UPDATE libraries SET name=? WHERE id=?`, name, id)
	if err != nil {
		return Library{}, fmt.Errorf("rename library: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Library{}, ErrLibraryNotFound
	}
	libraries, err := c.Libraries()
	if err != nil {
		return Library{}, err
	}
	for _, library := range libraries {
		if library.ID == id {
			return library, nil
		}
	}
	return Library{}, ErrLibraryNotFound
}

func (c *Catalog) DeleteLibrary(id string) error {
	if id == "" || c.db == nil {
		return ErrLibraryNotFound
	}
	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM library_locations WHERE library_id=?`, id).Scan(&count); err != nil {
		return fmt.Errorf("count library locations: %w", err)
	}
	if count != 0 {
		return ErrLibraryHasLocations
	}
	result, err := c.db.Exec(`DELETE FROM libraries WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete library: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrLibraryNotFound
	}
	return nil
}

func (c *Catalog) AddLibraryLocation(libraryID, rootPath string) (LibraryLocation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return LibraryLocation{}, ErrScanActive
	}
	if c.db == nil {
		return LibraryLocation{}, ErrInvalidLibrary
	}
	canonical, err := c.validateLocationPath("", rootPath)
	if err != nil {
		return LibraryLocation{}, err
	}
	var name, kind string
	if err := c.db.QueryRow(`SELECT name,kind FROM libraries WHERE id=?`, libraryID).Scan(&name, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LibraryLocation{}, ErrLibraryNotFound
		}
		return LibraryLocation{}, fmt.Errorf("load library: %w", err)
	}
	id, err := randomScanID()
	if err != nil {
		return LibraryLocation{}, fmt.Errorf("generate location ID: %w", err)
	}
	if _, err := c.db.Exec(`INSERT INTO library_locations(id,library_id,root_path) VALUES(?,?,?)`, id, libraryID, canonical); err != nil {
		return LibraryLocation{}, fmt.Errorf("add library location: %w", err)
	}
	return LibraryLocation{ID: id, LibraryID: libraryID, LibraryName: name, RootPath: canonical, Revision: 1, State: "unknown"}, nil
}

// PreviewLocationChange records the exact physical sources affected by moving
// or removing a location. An empty new path previews removal.
func (c *Catalog) PreviewLocationChange(locationID, newRootPath string) (LocationChangePreview, error) {
	return c.previewLocationChange(locationID, newRootPath, "owner")
}

func (c *Catalog) previewLocationChange(locationID, newRootPath, origin string) (LocationChangePreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return LocationChangePreview{}, ErrScanActive
	}
	if c.db == nil || locationID == "" {
		return LocationChangePreview{}, ErrLocationNotFound
	}
	var revision int64
	var current string
	if err := c.db.QueryRow(`SELECT revision,root_path FROM library_locations WHERE id=?`, locationID).Scan(&revision, &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LocationChangePreview{}, ErrLocationNotFound
		}
		return LocationChangePreview{}, fmt.Errorf("load library location: %w", err)
	}
	canonical := ""
	var err error
	if strings.TrimSpace(newRootPath) != "" {
		canonical, err = c.validateLocationPath(locationID, newRootPath)
		if err != nil {
			return LocationChangePreview{}, err
		}
		if canonical == current {
			return LocationChangePreview{}, ErrInvalidLibrary
		}
	}
	previewID, err := randomScanID()
	if err != nil {
		return LocationChangePreview{}, fmt.Errorf("generate location preview ID: %w", err)
	}
	tx, err := c.db.Begin()
	if err != nil {
		return LocationChangePreview{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM library_location_removal_previews WHERE location_id=?`, locationID); err != nil {
		return LocationChangePreview{}, fmt.Errorf("replace location preview: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO library_location_removal_previews(id,location_id,location_revision,new_root_path,origin,created_at) VALUES(?,?,?,?,?,?)`, previewID, locationID, revision, canonical, origin, time.Now().UnixMilli()); err != nil {
		return LocationChangePreview{}, fmt.Errorf("save location preview: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO library_location_removal_preview_sources(preview_id,physical_file_id,catalog_id) SELECT ?,id,catalog_id FROM catalog_physical_files WHERE location_id=? AND present=1`, previewID, locationID); err != nil {
		return LocationChangePreview{}, fmt.Errorf("save location preview sources: %w", err)
	}
	var sources, titles int
	if err := tx.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT catalog_id) FROM library_location_removal_preview_sources WHERE preview_id=?`, previewID).Scan(&sources, &titles); err != nil {
		return LocationChangePreview{}, err
	}
	if _, err := tx.Exec(`UPDATE library_location_removal_previews SET source_count=? WHERE id=?`, sources, previewID); err != nil {
		return LocationChangePreview{}, err
	}
	if err := tx.Commit(); err != nil {
		return LocationChangePreview{}, err
	}
	return LocationChangePreview{ID: previewID, LocationID: locationID, NewRootPath: canonical, AffectedSources: sources, AffectedTitles: titles}, nil
}

func (c *Catalog) CancelLocationChange(previewID string) error {
	if c.db == nil || previewID == "" {
		return ErrRemovalReviewNotFound
	}
	result, err := c.db.Exec(`DELETE FROM library_location_removal_previews WHERE id=?`, previewID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrRemovalReviewNotFound
	}
	return nil
}

func (c *Catalog) ConfirmLocationChange(ctx context.Context, previewID string) error {
	if c.db == nil || previewID == "" {
		return ErrRemovalReviewNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return ErrScanActive
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locationID, newRootPath string
	var expectedRevision, currentRevision int64
	var expectedSources int
	if err := tx.QueryRowContext(ctx, `SELECT p.location_id,p.location_revision,p.new_root_path,p.source_count,x.revision FROM library_location_removal_previews p JOIN library_locations x ON x.id=p.location_id WHERE p.id=?`, previewID).Scan(&locationID, &expectedRevision, &newRootPath, &expectedSources, &currentRevision); err != nil {
		return ErrRemovalReviewNotFound
	}
	if expectedRevision != currentRevision {
		return ErrRemovalReviewNotFound
	}
	if newRootPath != "" {
		rows, err := tx.QueryContext(ctx, `SELECT id,root_path FROM library_locations WHERE id<>?`, locationID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, existing string
			if err := rows.Scan(&id, &existing); err != nil {
				rows.Close()
				return err
			}
			if pathsOverlap(newRootPath, existing) {
				rows.Close()
				return ErrOverlappingLocation
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	var currentSources, previewSources, changed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_physical_files WHERE location_id=? AND present=1`, locationID).Scan(&currentSources); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_location_removal_preview_sources WHERE preview_id=?`, previewID).Scan(&previewSources); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_physical_files f WHERE f.location_id=? AND f.present=1 AND NOT EXISTS (SELECT 1 FROM library_location_removal_preview_sources p WHERE p.preview_id=? AND p.physical_file_id=f.id)`, locationID, previewID).Scan(&changed); err != nil || changed != 0 || currentSources != expectedSources || previewSources != expectedSources {
		return ErrRemovalReviewNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT catalog_id FROM library_location_removal_preview_sources WHERE preview_id=?`, previewID)
	if err != nil {
		return err
	}
	var catalogIDs []string
	for rows.Next() {
		var catalogID string
		if err := rows.Scan(&catalogID); err != nil {
			rows.Close()
			return err
		}
		catalogIDs = append(catalogIDs, catalogID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_physical_files SET present=0,selected=0 WHERE location_id=? AND present=1`, locationID); err != nil {
		return err
	}
	switched := make(map[string]bool, len(catalogIDs))
	for _, catalogID := range catalogIDs {
		changed, err := reselectCatalogSource(ctx, tx, catalogID)
		if err != nil {
			return err
		}
		switched[catalogID] = changed
	}
	if newRootPath == "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_locations WHERE id=?`, locationID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE library_locations SET root_path=?,revision=revision+1,state='unknown',scan_complete=0,item_count=0,missing_count=0,pending_scan_id='',message='' WHERE id=?`, newRootPath, locationID); err != nil {
			return err
		}
	}
	if locationID == "films-root" || locationID == "tv-root" {
		key := "film_root"
		if locationID == "tv-root" {
			key = "tv_root"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, newRootPath); err != nil {
			return err
		}
	}
	if err := applyIdentityMappings(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if locationID == "films-root" {
		c.film = newRootPath
	}
	if locationID == "tv-root" {
		c.tv = newRootPath
	}
	if err := c.refreshReselectedItems(catalogIDs, switched); err != nil {
		return err
	}
	c.refreshSeriesAvailability()
	return nil
}

func reselectCatalogSource(ctx context.Context, tx *sql.Tx, catalogID string) (bool, error) {
	var primaryPresent bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_physical_files f JOIN catalog_items i ON i.primary_file_id=f.id WHERE i.id=? AND f.present=1)`, catalogID).Scan(&primaryPresent); err != nil {
		return false, err
	}
	if primaryPresent {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_items SET available=1 WHERE id=?`, catalogID)
		return false, err
	}
	var physicalID, alternateLocation, rootKind, relativePath, fingerprint string
	var size, mtime int64
	err := tx.QueryRowContext(ctx, `SELECT id,location_id,root_kind,relative_path,fingerprint,size_bytes,mtime_unix FROM catalog_physical_files WHERE catalog_id=? AND present=1 ORDER BY location_id,relative_path,id LIMIT 1`, catalogID).Scan(&physicalID, &alternateLocation, &rootKind, &relativePath, &fingerprint, &size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_items SET available=0 WHERE id=?`, catalogID)
		return false, err
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_physical_files SET selected=(id=?) WHERE catalog_id=?`, physicalID, catalogID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_items SET primary_file_id=?,source_location_id=?,root_kind=?,relative_path=?,fingerprint=?,size_bytes=?,mtime_unix=?,available=1 WHERE id=?`, physicalID, alternateLocation, rootKind, relativePath, fingerprint, size, mtime, catalogID); err != nil {
		return false, err
	}
	// Sidecars belong to the former primary location. A later scan may admit
	// sidecars beside the alternate, but carrying these paths across roots
	// would let an old selection name a different file.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_audio_sidecars WHERE catalog_id=?; DELETE FROM catalog_subtitle_sidecars WHERE catalog_id=?`, catalogID, catalogID); err != nil {
		return false, err
	}
	return true, nil
}

// Called under c.mu after the source-selection transaction commits.
func (c *Catalog) refreshReselectedItems(catalogIDs []string, switched map[string]bool) error {
	for _, catalogID := range catalogIDs {
		item := c.items[catalogID]
		if switched[catalogID] {
			item.Audio = embeddedAudio(item.Audio)
			item.Subtitles = embeddedSubtitles(item.Subtitles)
			item.sourceRoot = ""
			if err := c.db.QueryRow(`SELECT i.relative_path,i.root_kind,i.source_location_id,i.fingerprint,i.size_bytes,i.mtime_unix,i.playable,f.full_digest,f.change_token,f.source_series_id FROM catalog_items i JOIN catalog_physical_files f ON f.id=i.primary_file_id WHERE i.id=?`, catalogID).Scan(&item.path, &item.rootKind, &item.sourceLocationID, &item.fingerprint, &item.size, &item.mtime, &item.Playable, &item.digest, &item.changeToken, &item.sourceSeriesID); err != nil {
				return err
			}
		} else if err := c.db.QueryRow(`SELECT playable FROM catalog_items WHERE id=?`, catalogID).Scan(&item.Playable); err != nil {
			return err
		}
		c.items[catalogID] = item
	}
	return nil
}

func (c *Catalog) validateLocationPath(excludeID, rootPath string) (string, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return "", ErrOutsideRoot
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	info, err := c.fs.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("root %q: %w", rootPath, ErrOutsideRoot)
	}
	canonical := filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(canonical); resolveErr == nil {
		canonical = filepath.Clean(resolved)
	}
	if c.db == nil {
		return canonical, nil
	}
	rows, err := c.db.Query(`SELECT id,root_path FROM library_locations WHERE id<>?`, excludeID)
	if err != nil {
		return "", fmt.Errorf("load location paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return "", err
		}
		existingCanonical := filepath.Clean(existing)
		if resolved, resolveErr := filepath.EvalSymlinks(existingCanonical); resolveErr == nil {
			existingCanonical = filepath.Clean(resolved)
		}
		if pathsOverlap(canonical, existingCanonical) {
			return "", fmt.Errorf("%w: %s", ErrOverlappingLocation, id)
		}
	}
	return canonical, rows.Err()
}

func pathsOverlap(left, right string) bool {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}

func (c *Catalog) scanRoots() ([]scanRoot, error) {
	if c.db == nil {
		return []scanRoot{{id: "films-root", libraryID: "films", libraryName: "Films", path: c.film, kind: "film"}, {id: "tv-root", libraryID: "tv", libraryName: "TV", path: c.tv, kind: "episode"}}, nil
	}
	rows, err := c.db.Query(`SELECT x.id,x.library_id,l.name,x.root_path,l.kind FROM library_locations x JOIN libraries l ON l.id=x.library_id WHERE x.state<>'topology_review' ORDER BY l.created_at,l.id,x.id`)
	if err != nil {
		return nil, fmt.Errorf("load scan locations: %w", err)
	}
	defer rows.Close()
	var roots []scanRoot
	for rows.Next() {
		var root scanRoot
		if err := rows.Scan(&root.id, &root.libraryID, &root.libraryName, &root.path, &root.kind); err != nil {
			return nil, fmt.Errorf("read scan locations: %w", err)
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		var configured int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM library_locations`).Scan(&configured); err != nil {
			return nil, err
		}
		if configured == 0 {
			if c.film != "" {
				roots = append(roots, scanRoot{id: "films-root", libraryID: "films", libraryName: "Films", path: c.film, kind: "film"})
			}
			if c.tv != "" {
				roots = append(roots, scanRoot{id: "tv-root", libraryID: "tv", libraryName: "TV", path: c.tv, kind: "episode"})
			}
		}
	}
	return roots, nil
}

func (c *Catalog) locationRoot(id string) (string, error) {
	if c.db == nil {
		if id == "films-root" {
			return c.film, nil
		}
		if id == "tv-root" {
			return c.tv, nil
		}
		return "", ErrLocationNotFound
	}
	var root string
	if err := c.db.QueryRow(`SELECT root_path FROM library_locations WHERE id=?`, id).Scan(&root); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if id == "films-root" && c.film != "" {
				return c.film, nil
			}
			if id == "tv-root" && c.tv != "" {
				return c.tv, nil
			}
			return "", ErrLocationNotFound
		}
		return "", err
	}
	return root, nil
}

func (c *Catalog) sourceRoot(item Item) (string, error) {
	locationID := item.sourceLocationID
	if locationID == "" {
		if item.rootKind == "film" {
			locationID = "films-root"
		} else if item.rootKind == "episode" {
			locationID = "tv-root"
		}
	}
	root, err := c.locationRoot(locationID)
	if errors.Is(err, ErrLocationNotFound) && item.probeRevision == 0 {
		return "", nil
	}
	return root, err
}
