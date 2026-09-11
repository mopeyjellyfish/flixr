package catalog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const maxLocalNFOBytes = 512 << 10

type localNFO struct {
	Fields            map[string]string
	ProviderID        string
	Ignored           []string
	Invalid           []string
	providerIDInvalid bool
}

type localCandidates struct {
	nfo, poster, backdrop []string
}

type localDocumentState struct {
	fields, fallback                     map[string]string
	providerID, fallbackProviderID       string
	fallbackProvider, locationID, source string
}

type localImportPlan struct {
	kind, id, locationID, source, fingerprint string
	fields, fallback                          map[string]string
	previous                                  map[string]string
	providerID, fallbackProviderID            string
	previousProviderID                        string
	fallbackProvider                          string
	remove                                    bool
	clearProviderArtwork                      bool
	providerArtwork                           map[string]Artwork
	lockedArtwork                             map[string]bool
	artwork                                   map[string]localArtworkPlan
}

type localArtworkPlan struct {
	source, fingerprint string
	artwork             Artwork
	fallbackValue       string
	remove              bool
}

type localTarget struct{ kind, id, locationID, root, mediaPath string }
type localNFOStage struct {
	source, fingerprint  string
	parsed               localNFO
	found                bool
	err                  error
	identityIntent       bool
	identityProviderID   string
	priorProviderID      string
	priorProvider        string
	priorFields          map[string]string
	clearProviderArtwork bool
	providerArtwork      map[string]Artwork
}

func parseLocalNFO(ctx context.Context, kind string, input io.Reader) (localNFO, error) {
	limited := io.LimitReader(input, maxLocalNFOBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return localNFO{}, fmt.Errorf("read local NFO: %w", err)
	}
	if len(raw) > maxLocalNFOBytes {
		return localNFO{}, errors.New("local NFO exceeds 512 KiB")
	}
	upper := strings.ToUpper(string(raw))
	if strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY") {
		return localNFO{}, errors.New("local NFO document type and custom entity declarations are not allowed")
	}
	wantRoot := map[string]string{"film": "movie", "series": "tvshow", "episode": "episodedetails"}[kind]
	if wantRoot == "" {
		return localNFO{}, errors.New("unsupported local NFO media kind")
	}
	result := localNFO{Fields: map[string]string{}}
	decoder := xml.NewDecoder(bufio.NewReader(strings.NewReader(string(raw))))
	decoder.Strict = true
	depth := 0
	root := ""
	rootComplete := false
	var tags, ignored []string
	var yearFallback string
	providerIDs := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return localNFO{}, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return localNFO{}, fmt.Errorf("parse local NFO XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 && rootComplete {
				return localNFO{}, errors.New("local NFO must contain exactly one XML document root")
			}
			depth++
			name := strings.ToLower(value.Name.Local)
			if depth == 1 {
				root = name
				if root != wantRoot {
					return localNFO{}, fmt.Errorf("local NFO root <%s> does not match %s", root, kind)
				}
				continue
			}
			if depth != 2 {
				continue
			}
			var text string
			if err := decoder.DecodeElement(&text, &value); err != nil {
				return localNFO{}, fmt.Errorf("parse local NFO <%s>: %w", name, err)
			}
			depth--
			text = strings.TrimSpace(text)
			switch name {
			case "title":
				result.Fields["title"] = text
			case "plot":
				result.Fields["synopsis"] = text
			case "year":
				if text != "" {
					if _, err := strconv.Atoi(text); err != nil {
						result.Invalid = append(result.Invalid, "year")
						break
					}
					result.Fields["year"] = text
				}
			case "premiered", "aired":
				if len(text) >= 4 {
					if _, err := strconv.Atoi(text[:4]); err == nil {
						yearFallback = text[:4]
					}
				}
			case "mpaa":
				if kind == "episode" {
					ignored = append(ignored, name)
				} else {
					result.Fields["content_rating"] = text
				}
			case "tag":
				if kind == "episode" {
					ignored = append(ignored, name)
				} else if text != "" && !containsString(tags, text) {
					tags = append(tags, text)
				}
			case "uniqueid":
				namespace := ""
				for _, attr := range value.Attr {
					if strings.EqualFold(attr.Name.Local, "type") {
						namespace = strings.ToLower(strings.TrimSpace(attr.Value))
					}
				}
				if namespace == "tmdb" && providerIdentity(namespace, kind, text) == "" {
					result.Invalid = append(result.Invalid, "uniqueid")
					result.providerIDInvalid = true
				} else if namespace != "tmdb" {
					ignored = append(ignored, "uniqueid")
				} else {
					providerIDs[text] = true
				}
			default:
				ignored = append(ignored, name)
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				rootComplete = true
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return localNFO{}, errors.New("local NFO contains text outside its XML document root")
			}
		case xml.Comment, xml.Directive:
			if depth == 0 {
				return localNFO{}, errors.New("local NFO must contain only whitespace outside its XML document root")
			}
		case xml.ProcInst:
			if depth == 0 && (!strings.EqualFold(value.Target, "xml") || root != "") {
				return localNFO{}, errors.New("local NFO must contain only whitespace outside its XML document root")
			}
		}
	}
	if root == "" || !rootComplete {
		return localNFO{}, errors.New("local NFO is empty")
	}
	if result.Fields["year"] == "" && yearFallback != "" {
		result.Fields["year"] = yearFallback
	}
	if len(tags) > 0 {
		result.Fields["tags"] = strings.Join(tags, ",")
	}
	if len(providerIDs) > 1 {
		return localNFO{}, errors.New("local NFO contains conflicting tmdb unique identifiers")
	}
	for value := range providerIDs {
		result.ProviderID = value
	}
	sort.Strings(ignored)
	result.Ignored = compactStrings(ignored)
	sort.Strings(result.Invalid)
	result.Invalid = compactStrings(result.Invalid)
	return result, nil
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func localSidecarCandidates(kind, mediaPath string, movieCount map[string]int) localCandidates {
	dir := path.Dir(mediaPath)
	if kind == "series" {
		dir = strings.TrimSuffix(mediaPath, "/")
		return localCandidates{nfo: []string{path.Join(dir, "tvshow.nfo")}, poster: artworkCandidates(dir, "poster"), backdrop: append(artworkCandidates(dir, "fanart"), artworkCandidates(dir, "backdrop")...)}
	}
	base := strings.TrimSuffix(path.Base(mediaPath), path.Ext(mediaPath))
	prefix := path.Join(dir, base)
	result := localCandidates{
		nfo:      []string{prefix + ".nfo"},
		poster:   append(artworkCandidates(dir, base+"-poster"), artworkCandidates(dir, base+"-thumb")...),
		backdrop: append(artworkCandidates(dir, base+"-fanart"), artworkCandidates(dir, base+"-backdrop")...),
	}
	if kind == "film" && movieCount[dir] == 1 {
		result.nfo = append(result.nfo, path.Join(dir, "movie.nfo"))
		result.poster = append(result.poster, artworkCandidates(dir, "poster")...)
		result.backdrop = append(result.backdrop, artworkCandidates(dir, "fanart")...)
		result.backdrop = append(result.backdrop, artworkCandidates(dir, "backdrop")...)
	}
	return result
}

func artworkCandidates(dir, stem string) []string {
	out := make([]string, 0, 4)
	for _, extension := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		out = append(out, path.Join(dir, stem+extension))
	}
	return out
}

func (c *Catalog) localTargets(items map[string]Item, scannedLocations map[string]bool) ([]localTarget, map[string]map[string]int) {
	movieCounts := map[string]map[string]int{}
	for _, item := range items {
		if item.Kind != "film" {
			continue
		}
		if movieCounts[item.sourceLocationID] == nil {
			movieCounts[item.sourceLocationID] = map[string]int{}
		}
		movieCounts[item.sourceLocationID][path.Dir(item.path)]++
	}
	targets := make([]localTarget, 0, len(items))
	seriesSource := map[string]localTarget{}
	for _, item := range items {
		if !scannedLocations[item.sourceLocationID] {
			continue
		}
		root, rootErr := c.sourceRoot(item)
		if rootErr != nil {
			continue
		}
		targets = append(targets, localTarget{item.Kind, item.ID, item.sourceLocationID, root, item.path})
		if item.Kind == "episode" {
			parts := strings.Split(filepath.ToSlash(item.path), "/")
			if len(parts) > 1 {
				candidate := localTarget{"series", item.SeriesID, item.sourceLocationID, root, parts[0]}
				current, ok := seriesSource[item.SeriesID]
				if !ok || candidate.locationID+"/"+candidate.mediaPath < current.locationID+"/"+current.mediaPath {
					seriesSource[item.SeriesID] = candidate
				}
			}
		}
	}
	for _, source := range seriesSource {
		targets = append(targets, source)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].locationID+"/"+targets[i].mediaPath+"/"+targets[i].kind < targets[j].locationID+"/"+targets[j].mediaPath+"/"+targets[j].kind
	})
	return targets, movieCounts
}

func (c *Catalog) localIdentityHints(ctx context.Context, items map[string]Item, scannedLocations, completeLocations map[string]bool) (map[string]string, map[string]localNFOStage, error) {
	targets, movieCounts := c.localTargets(items, scannedLocations)
	hints := map[string]string{}
	stages := map[string]localNFOStage{}
	c.mu.RLock()
	previousItems := make(map[string]Item, len(c.items))
	for id, value := range c.items {
		previousItems[id] = value
	}
	previousSeries := make(map[string]Series, len(c.series))
	for id, value := range c.series {
		previousSeries[id] = value
	}
	c.mu.RUnlock()
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		candidates := localSidecarCandidates(target.kind, target.mediaPath, movieCounts[target.locationID])
		source, found, candidateErr := firstRegularLocalFile(target.root, candidates.nfo)
		stage := localNFOStage{source: source, found: found, err: candidateErr}
		if candidateErr == nil && found {
			stage.parsed, stage.fingerprint, stage.err = readLocalNFO(ctx, target.kind, target.root, source)
		}
		key := refreshKey(target.kind, target.id)
		stages[key] = stage
		if stage.err != nil || stage.parsed.providerIDInvalid {
			continue
		}
		state, hasState, stateErr := c.loadLocalDocument(target.kind, target.id)
		if stateErr != nil {
			return nil, nil, stateErr
		}
		desiredID, hasIntent := stage.parsed.ProviderID, stage.parsed.ProviderID != ""
		if !hasIntent && hasState && state.providerID != "" && (found || completeLocations[target.locationID]) {
			desiredID, hasIntent = state.fallbackProviderID, true
		}
		if !hasIntent {
			continue
		}
		ownerMatch, ownerUnmatch, currentID, currentProvider := false, false, "", ""
		var priorFields map[string]string
		if target.kind == "series" {
			value := previousSeries[target.id]
			ownerMatch, ownerUnmatch, currentID, currentProvider = value.OwnerMatch, value.OwnerUnmatch, value.ProviderID, value.Provider
			priorFields = itemFieldValues(Item{Title: value.Title, Synopsis: value.Synopsis, Year: value.Year, Poster: value.Poster, Backdrop: value.Backdrop})
		} else {
			value, ok := previousItems[target.id]
			if !ok {
				value = items[target.id]
			}
			ownerMatch, ownerUnmatch, currentID, currentProvider = value.OwnerMatch, value.OwnerUnmatch, value.ProviderID, value.Provider
			priorFields = itemFieldValues(value)
		}
		if ownerUnmatch || (ownerMatch && providerIdentity("tmdb", target.kind, currentID) != providerIdentity("tmdb", target.kind, desiredID)) {
			continue
		}
		if providerIdentity("tmdb", target.kind, currentID) == providerIdentity("tmdb", target.kind, desiredID) {
			continue
		}
		stage.identityIntent = true
		stage.identityProviderID = desiredID
		stage.priorProviderID = currentID
		stage.priorProvider = currentProvider
		stage.priorFields = priorFields
		stage.clearProviderArtwork = currentID != ""
		stages[key] = stage
		if desiredID != "" {
			hints[key] = desiredID
		}
	}
	return hints, stages, nil
}

func (c *Catalog) prepareLocalMetadata(ctx context.Context, items map[string]Item, series map[string]Series, scannedLocations, completeLocations map[string]bool, stages map[string]localNFOStage) ([]localImportPlan, []scanObservation, error) {
	targets, movieCounts := c.localTargets(items, scannedLocations)
	var plans []localImportPlan
	var observations []scanObservation
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		state, hasState, err := c.loadLocalDocument(target.kind, target.id)
		if err != nil {
			return nil, nil, err
		}
		candidates := localSidecarCandidates(target.kind, target.mediaPath, movieCounts[target.locationID])
		stage := stages[refreshKey(target.kind, target.id)]
		nfoPath, found, candidateErr := stage.source, stage.found, stage.err
		lockedFields := c.lockedMetadata(target.kind, target.id)
		plan := localImportPlan{kind: target.kind, id: target.id, locationID: target.locationID, fields: map[string]string{}, fallback: map[string]string{}, previous: cloneStrings(state.fields), previousProviderID: state.providerID, clearProviderArtwork: stage.clearProviderArtwork, providerArtwork: stage.providerArtwork, lockedArtwork: lockedFields, artwork: map[string]localArtworkPlan{}}
		if candidateErr != nil {
			observations = append(observations, localObservation(target.locationID, target.kind, nfoPath, "local_metadata_invalid", candidateErr.Error()))
		} else if found {
			parsed, fingerprint, parseErr := stage.parsed, stage.fingerprint, stage.err
			if parseErr != nil {
				observations = append(observations, localObservation(target.locationID, target.kind, nfoPath, "local_metadata_invalid", parseErr.Error()))
			} else {
				if parsed.providerIDInvalid && state.providerID != "" {
					parsed.ProviderID = state.providerID
				}
				for _, field := range parsed.Invalid {
					if previous, ok := state.fields[field]; ok {
						parsed.Fields[field] = previous
					}
				}
				ownerMatch, ownerUnmatch, currentProviderID := localTargetIdentity(target.kind, target.id, items, series)
				if parsed.ProviderID != "" && (ownerUnmatch || (ownerMatch && currentProviderID != parsed.ProviderID)) {
					observations = append(observations, localObservation(target.locationID, target.kind, nfoPath, "local_metadata_ignored", "The tmdb unique identifier conflicts with the owner's match choice and was ignored."))
					parsed.ProviderID = ""
				}
				plan.source, plan.fingerprint, plan.fields, plan.providerID = nfoPath, fingerprint, parsed.Fields, parsed.ProviderID
				plan.fallback, plan.fallbackProviderID, plan.fallbackProvider = localFallback(target.kind, target.id, plan.fields, items, series, state, hasState)
				if !hasState && stage.identityIntent && plan.providerID != "" {
					plan.fallback = cloneStrings(stage.priorFields)
					plan.fallbackProviderID, plan.fallbackProvider = stage.priorProviderID, stage.priorProvider
				}
				applyLocalPlan(&plan, items, series, c.lockedMetadata(target.kind, target.id))
				plans = append(plans, plan)
				if len(parsed.Ignored) > 0 {
					observations = append(observations, localObservation(target.locationID, target.kind, nfoPath, "local_metadata_ignored", "Unsupported NFO fields were ignored: "+strings.Join(parsed.Ignored, ", ")+"."))
				}
				if len(parsed.Invalid) > 0 {
					observations = append(observations, localObservation(target.locationID, target.kind, nfoPath, "local_metadata_invalid", "Invalid NFO fields retained their last good values: "+strings.Join(parsed.Invalid, ", ")+"."))
				}
			}
		} else if hasState && completeLocations[target.locationID] {
			plan.remove, plan.fields, plan.fallback = true, state.fields, state.fallback
			plan.fallbackProviderID, plan.fallbackProvider = state.fallbackProviderID, state.fallbackProvider
			applyLocalPlan(&plan, items, series, c.lockedMetadata(target.kind, target.id))
			plans = append(plans, plan)
		}
		for artworkKind, paths := range map[string][]string{"poster": candidates.poster, "backdrop": candidates.backdrop} {
			assetPath, exists, candidateErr := firstRegularLocalFile(target.root, paths)
			hasAsset, priorFallbackValue, err := c.localArtworkState(target.kind, target.id, artworkKind)
			if err != nil {
				return nil, nil, err
			}
			if lockedFields[artworkKind] {
				continue
			}
			if candidateErr != nil {
				observations = append(observations, localObservation(target.locationID, target.kind, assetPath, "local_artwork_invalid", candidateErr.Error()))
				continue
			}
			if exists {
				art, fingerprint, artErr := readLocalArtwork(ctx, target.root, assetPath)
				if artErr != nil {
					observations = append(observations, localObservation(target.locationID, target.kind, assetPath, "local_artwork_invalid", artErr.Error()))
					continue
				}
				assetPlan := localArtworkPlan{source: assetPath, fingerprint: fingerprint, artwork: art, fallbackValue: currentArtworkValue(target.kind, target.id, artworkKind, items, series)}
				if len(plans) == 0 || plans[len(plans)-1].kind != target.kind || plans[len(plans)-1].id != target.id {
					plans = append(plans, plan)
				}
				plans[len(plans)-1].artwork[artworkKind] = assetPlan
				setArtworkValue(target.kind, target.id, artworkKind, artworkURL(target.id, artworkKind), items, series, false)
			} else if hasAsset && completeLocations[target.locationID] {
				if len(plans) == 0 || plans[len(plans)-1].kind != target.kind || plans[len(plans)-1].id != target.id {
					plans = append(plans, plan)
				}
				plans[len(plans)-1].artwork[artworkKind] = localArtworkPlan{remove: true, fallbackValue: priorFallbackValue}
				setArtworkValue(target.kind, target.id, artworkKind, priorFallbackValue, items, series, false)
			}
		}
	}
	return plans, observations, nil
}

func localTargetIdentity(kind, id string, items map[string]Item, series map[string]Series) (bool, bool, string) {
	if kind == "series" {
		x := series[id]
		return x.OwnerMatch, x.OwnerUnmatch, x.ProviderID
	}
	x := items[id]
	return x.OwnerMatch, x.OwnerUnmatch, x.ProviderID
}

func firstRegularLocalFile(root string, candidates []string) (string, bool, error) {
	for _, candidate := range candidates {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(candidate)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return candidate, false, err
		}
		if !info.Mode().IsRegular() {
			return candidate, false, errors.New("local sidecar must be a regular non-symlink file")
		}
		return candidate, true, nil
	}
	return "", false, nil
}

func readLocalNFO(ctx context.Context, kind, rootPath, relative string) (localNFO, string, error) {
	before, err := localFileSnapshot(rootPath, relative)
	if err != nil {
		return localNFO{}, "", err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return localNFO{}, "", err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return localNFO{}, "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxLocalNFOBytes+1))
	if err != nil {
		return localNFO{}, "", err
	}
	if err := validateLocalFileSnapshot(file, before); err != nil {
		return localNFO{}, "", err
	}
	parsed, err := parseLocalNFO(ctx, kind, bytes.NewReader(raw))
	if err != nil {
		return localNFO{}, "", err
	}
	return parsed, fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func readLocalArtwork(ctx context.Context, rootPath, relative string) (Artwork, string, error) {
	if err := ctx.Err(); err != nil {
		return Artwork{}, "", err
	}
	before, err := localFileSnapshot(rootPath, relative)
	if err != nil {
		return Artwork{}, "", err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Artwork{}, "", err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return Artwork{}, "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxArtworkSource+1))
	if err != nil {
		return Artwork{}, "", err
	}
	if len(raw) > maxArtworkSource {
		return Artwork{}, "", errors.New("local artwork exceeds 5 MiB")
	}
	if err := validateLocalFileSnapshot(file, before); err != nil {
		return Artwork{}, "", err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Artwork{}, "", errors.New("local artwork is corrupt or unsupported")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxArtworkPixels/config.Height || config.Width > maxArtworkWidth*4 || config.Height > maxArtworkHeight*4 {
		return Artwork{}, "", errors.New("local artwork dimensions exceed the safe limit")
	}
	contentType := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp"}[format]
	if !allowedArtworkContentType(contentType) {
		return Artwork{}, "", errors.New("local artwork format is unsupported")
	}
	return Artwork{Bytes: raw, ContentType: contentType}, fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// stageLocalProviderArtwork keeps an identity transition's provider bytes out of
// the published cache until the scan transaction commits the matching identity.
func (c *Catalog) stageLocalProviderArtwork(ctx context.Context, kind, id string, enrichment Enrichment) (map[string]Artwork, bool) {
	c.mu.RLock()
	provider, ok := c.provider.(ArtworkProvider)
	c.mu.RUnlock()
	staged := map[string]Artwork{}
	if !ok || c.db == nil {
		return staged, enrichment.Poster != "" || enrichment.Backdrop != ""
	}
	locked := c.lockedMetadata(kind, id)
	failed := false
	for artworkKind, source := range map[string]string{"poster": enrichment.Poster, "backdrop": enrichment.Backdrop} {
		if source == "" || locked[artworkKind] {
			continue
		}
		art, err := provider.FetchArtwork(ctx, source)
		if err != nil || !allowedArtworkContentType(art.ContentType) || len(art.Bytes) == 0 {
			failed = true
			continue
		}
		staged[artworkKind] = art
	}
	return staged, failed
}

type localSnapshot struct {
	size, mtime int64
	token       string
}

func localFileSnapshot(root, relative string) (localSnapshot, error) {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return localSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return localSnapshot{}, errors.New("local sidecar must be a regular non-symlink file")
	}
	return localSnapshot{info.Size(), info.ModTime().UnixNano(), fileChangeToken(info)}, nil
}

func validateLocalFileSnapshot(file *os.File, before localSnapshot) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if after.Size() != before.size || after.ModTime().UnixNano() != before.mtime || fileChangeToken(after) != before.token {
		return errors.New("local sidecar changed while it was being read; scan again")
	}
	return nil
}

func localObservation(locationID, kind, relative, outcome, message string) scanObservation {
	return scanObservation{identifier: locationID + "\t" + kind + ":" + relative, outcome: outcome, message: outcome + ": " + message}
}

func (c *Catalog) loadLocalDocument(kind, id string) (localDocumentState, bool, error) {
	state := localDocumentState{fields: map[string]string{}, fallback: map[string]string{}}
	if c.db == nil {
		return state, false, nil
	}
	var fieldsJSON, fallbackJSON string
	err := c.db.QueryRow(`SELECT location_id,relative_path,fields_json,fallback_json,provider_id,fallback_provider_id,fallback_provider FROM catalog_local_metadata WHERE catalog_kind=? AND catalog_id=?`, kind, id).Scan(&state.locationID, &state.source, &fieldsJSON, &fallbackJSON, &state.providerID, &state.fallbackProviderID, &state.fallbackProvider)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if jsonErr := json.Unmarshal([]byte(fieldsJSON), &state.fields); jsonErr != nil {
		return state, false, jsonErr
	}
	if jsonErr := json.Unmarshal([]byte(fallbackJSON), &state.fallback); jsonErr != nil {
		return state, false, jsonErr
	}
	return state, true, nil
}

func (c *Catalog) localArtworkState(kind, id, artworkKind string) (bool, string, error) {
	if c.db == nil {
		return false, "", nil
	}
	var fallback string
	err := c.db.QueryRow(`SELECT fallback_value FROM catalog_local_artwork WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, kind, id, artworkKind).Scan(&fallback)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	return err == nil, fallback, err
}

func (c *Catalog) hasLocalArtwork(kind, id, artworkKind string) (bool, error) {
	present, _, err := c.localArtworkState(kind, id, artworkKind)
	return present, err
}

func currentArtworkValue(kind, id, artworkKind string, items map[string]Item, series map[string]Series) string {
	if kind == "series" {
		x := series[id]
		if artworkKind == "poster" {
			return x.Poster
		}
		return x.Backdrop
	}
	x := items[id]
	if artworkKind == "poster" {
		return x.Poster
	}
	return x.Backdrop
}

func itemFieldValues(item Item) map[string]string {
	return map[string]string{"title": item.Title, "synopsis": item.Synopsis, "year": strconv.Itoa(item.Year), "poster": item.Poster, "backdrop": item.Backdrop}
}

func localFallback(kind, id string, localFields map[string]string, items map[string]Item, series map[string]Series, prior localDocumentState, hasPrior bool) (map[string]string, string, string) {
	var all map[string]string
	var providerID, provider string
	if kind == "series" {
		x := series[id]
		all, providerID, provider = itemFieldValues(Item{Title: x.Title, Synopsis: x.Synopsis, Year: x.Year, Poster: x.Poster, Backdrop: x.Backdrop}), x.ProviderID, x.Provider
	} else {
		x := items[id]
		all, providerID, provider = itemFieldValues(x), x.ProviderID, x.Provider
	}
	fallback := map[string]string{}
	if hasPrior {
		fallback = cloneStrings(prior.fallback)
		providerID, provider = prior.fallbackProviderID, prior.fallbackProvider
	}
	for field := range localFields {
		if _, known := fallback[field]; !known {
			fallback[field] = all[field]
		}
	}
	return fallback, providerID, provider
}

func cloneStrings(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *Catalog) lockedMetadata(kind, id string) map[string]bool {
	out := map[string]bool{}
	fields, _ := c.MetadataFields(kind, id)
	for _, field := range fields {
		if field.Locked {
			out[field.Field] = true
		}
	}
	return out
}

func (c *Catalog) protectedMetadataValues(kind, id string) map[string]string {
	out := map[string]string{}
	fields, _ := c.MetadataFields(kind, id)
	for _, field := range fields {
		if field.Locked || field.Source == "local" {
			out[field.Field] = field.Value
		}
	}
	return out
}

func applyLocalPlan(plan *localImportPlan, items map[string]Item, series map[string]Series, locked map[string]bool) {
	values := plan.fields
	providerID := plan.providerID
	provider := "tmdb"
	if plan.remove {
		values, providerID, provider = plan.fallback, plan.fallbackProviderID, plan.fallbackProvider
	} else if plan.previousProviderID != "" && plan.providerID == "" {
		providerID, provider = plan.fallbackProviderID, plan.fallbackProvider
	}
	if plan.kind == "series" {
		x := series[plan.id]
		item := Item{Title: x.Title, Synopsis: x.Synopsis, Year: x.Year, Poster: x.Poster, Backdrop: x.Backdrop}
		if !plan.remove {
			applyRemovedLocalFields(&item, plan.previous, plan.fields, plan.fallback, locked)
		}
		applyFieldValues(&item, values, locked)
		if !plan.remove && (plan.providerID != "" || plan.previousProviderID != "") {
			x.ProviderID, x.Provider = providerID, provider
		}
		if plan.remove {
			x.ProviderID, x.Provider = providerID, provider
		}
		x.Title, x.Synopsis, x.Year, x.Poster, x.Backdrop = item.Title, item.Synopsis, item.Year, item.Poster, item.Backdrop
		series[plan.id] = x
		return
	}
	x := items[plan.id]
	if !plan.remove {
		applyRemovedLocalFields(&x, plan.previous, plan.fields, plan.fallback, locked)
	}
	applyFieldValues(&x, values, locked)
	if !plan.remove && (plan.providerID != "" || plan.previousProviderID != "") {
		x.ProviderID, x.Provider = providerID, provider
	}
	if plan.remove {
		x.ProviderID, x.Provider = providerID, provider
	}
	items[plan.id] = x
}

func applyRemovedLocalFields(item *Item, previous, current, fallback map[string]string, locked map[string]bool) {
	for field := range previous {
		if _, retained := current[field]; !retained {
			applyFieldValues(item, map[string]string{field: fallback[field]}, locked)
		}
	}
}

func applyFieldValues(item *Item, values map[string]string, locked map[string]bool) {
	for field, value := range values {
		if locked[field] {
			continue
		}
		switch field {
		case "title":
			item.Title = value
		case "synopsis":
			item.Synopsis = value
		case "year":
			item.Year, _ = strconv.Atoi(value)
		case "poster":
			item.Poster = value
		case "backdrop":
			item.Backdrop = value
		}
	}
}

func setArtworkValue(kind, id, artworkKind, value string, items map[string]Item, series map[string]Series, locked bool) {
	if locked {
		return
	}
	if kind == "series" {
		x := series[id]
		if artworkKind == "poster" {
			x.Poster = value
		} else {
			x.Backdrop = value
		}
		series[id] = x
		return
	}
	x := items[id]
	if artworkKind == "poster" {
		x.Poster = value
	} else {
		x.Backdrop = value
	}
	items[id] = x
}

func (c *Catalog) persistLocalMetadataTx(tx *sql.Tx, plans []localImportPlan) (created, obsolete []string, err error) {
	for _, plan := range plans {
		if plan.clearProviderArtwork {
			for _, artworkKind := range []string{"poster", "backdrop"} {
				if plan.lockedArtwork[artworkKind] {
					continue
				}
				var oldObject string
				rowErr := tx.QueryRow(`SELECT object_name FROM catalog_artwork a WHERE catalog_id=? AND kind=? AND NOT EXISTS (SELECT 1 FROM catalog_local_artwork l WHERE l.catalog_kind=? AND l.catalog_id=? AND l.artwork_kind=a.kind)`, plan.id, artworkKind, plan.kind, plan.id).Scan(&oldObject)
				if rowErr != nil && rowErr != sql.ErrNoRows {
					err = rowErr
					return
				}
				if rowErr == nil {
					obsolete = append(obsolete, oldObject)
					if _, err = tx.Exec(`DELETE FROM catalog_artwork WHERE catalog_id=? AND kind=?`, plan.id, artworkKind); err != nil {
						return
					}
				}
				var oldFallback string
				fallbackErr := tx.QueryRow(`SELECT fallback_object_name FROM catalog_local_artwork WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind).Scan(&oldFallback)
				if fallbackErr != nil && fallbackErr != sql.ErrNoRows {
					err = fallbackErr
					return
				}
				if fallbackErr == nil {
					obsolete = append(obsolete, oldFallback)
					if _, err = tx.Exec(`UPDATE catalog_local_artwork SET fallback_object_name='',fallback_content_type='',fallback_value='' WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind); err != nil {
						return
					}
				}
				if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind); err != nil {
					return
				}
			}
		}
		for _, artworkKind := range []string{"poster", "backdrop"} {
			art, ok := plan.providerArtwork[artworkKind]
			if !ok {
				continue
			}
			var localObject, localType, oldFallback string
			localErr := tx.QueryRow(`SELECT l.local_object_name,a.content_type,l.fallback_object_name FROM catalog_local_artwork l JOIN catalog_artwork a ON a.catalog_id=l.catalog_id AND a.kind=l.artwork_kind WHERE l.catalog_kind=? AND l.catalog_id=? AND l.artwork_kind=?`, plan.kind, plan.id, artworkKind).Scan(&localObject, &localType, &oldFallback)
			if localErr != nil && localErr != sql.ErrNoRows {
				err = localErr
				return
			}
			name, previous, replaceErr := c.replaceArtwork(tx, plan.id, artworkKind, art)
			if replaceErr != nil {
				err = replaceErr
				return
			}
			created = append(created, name)
			if localErr == nil {
				if _, err = tx.Exec(`UPDATE catalog_artwork SET content_type=?,object_name=? WHERE catalog_id=? AND kind=?`, localType, localObject, plan.id, artworkKind); err != nil {
					return
				}
				if _, err = tx.Exec(`UPDATE catalog_local_artwork SET fallback_object_name=?,fallback_content_type=?,fallback_value=? WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, name, art.ContentType, artworkURL(plan.id, artworkKind), plan.kind, plan.id, artworkKind); err != nil {
					return
				}
				obsolete = append(obsolete, oldFallback)
			} else {
				obsolete = append(obsolete, previous)
			}
		}
		if plan.remove {
			for field, value := range plan.fallback {
				if err = restoreLocalFieldTx(tx, plan.kind, plan.id, field, value, plan.fallbackProvider != ""); err != nil {
					return
				}
			}
			if _, err = tx.Exec(`DELETE FROM catalog_local_metadata WHERE catalog_kind=? AND catalog_id=?`, plan.kind, plan.id); err != nil {
				return
			}
		} else if plan.source != "" {
			fieldsJSON, _ := json.Marshal(plan.fields)
			fallbackJSON, _ := json.Marshal(plan.fallback)
			if _, err = tx.Exec(`INSERT INTO catalog_local_metadata(catalog_kind,catalog_id,location_id,relative_path,fingerprint,fields_json,fallback_json,provider_id,fallback_provider_id,fallback_provider,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(catalog_kind,catalog_id) DO UPDATE SET location_id=excluded.location_id,relative_path=excluded.relative_path,fingerprint=excluded.fingerprint,fields_json=excluded.fields_json,provider_id=excluded.provider_id,updated_at=excluded.updated_at`, plan.kind, plan.id, plan.locationID, plan.source, plan.fingerprint, string(fieldsJSON), string(fallbackJSON), plan.providerID, plan.fallbackProviderID, plan.fallbackProvider, time.Now().Unix()); err != nil {
				return
			}
			if _, err = tx.Exec(`DELETE FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? AND source='local'`, plan.kind, plan.id); err != nil {
				return
			}
			for field := range plan.previous {
				if _, retained := plan.fields[field]; retained {
					continue
				}
				if err = restoreLocalFieldTx(tx, plan.kind, plan.id, field, plan.fallback[field], plan.fallbackProvider != ""); err != nil {
					return
				}
			}
			for field, value := range plan.fields {
				if _, err = tx.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES(?,?,?,?,'local',0) ON CONFLICT(catalog_kind,catalog_id,field) DO UPDATE SET value=CASE WHEN locked=0 THEN excluded.value ELSE value END,source=CASE WHEN locked=0 THEN excluded.source ELSE source END`, plan.kind, plan.id, field, value); err != nil {
					return
				}
			}
		}
		for artworkKind, asset := range plan.artwork {
			if asset.remove {
				var localObject, fallbackObject, fallbackType string
				rowErr := tx.QueryRow(`SELECT local_object_name,fallback_object_name,fallback_content_type FROM catalog_local_artwork WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind).Scan(&localObject, &fallbackObject, &fallbackType)
				if rowErr != nil && rowErr != sql.ErrNoRows {
					err = rowErr
					return
				}
				if fallbackObject != "" {
					_, err = tx.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES(?,?,?,?) ON CONFLICT(catalog_id,kind) DO UPDATE SET content_type=excluded.content_type,object_name=excluded.object_name`, plan.id, artworkKind, fallbackType, fallbackObject)
				} else {
					_, err = tx.Exec(`DELETE FROM catalog_artwork WHERE catalog_id=? AND kind=?`, plan.id, artworkKind)
				}
				if err != nil {
					return
				}
				if err = restoreLocalFieldTx(tx, plan.kind, plan.id, artworkKind, asset.fallbackValue, fallbackObject != ""); err != nil {
					return
				}
				if _, err = tx.Exec(`DELETE FROM catalog_local_artwork WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind); err != nil {
					return
				}
				obsolete = append(obsolete, localObject)
				continue
			}
			var oldLocal, fallbackObject, fallbackType, fallbackValue string
			rowErr := tx.QueryRow(`SELECT local_object_name,fallback_object_name,fallback_content_type,fallback_value FROM catalog_local_artwork WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, plan.kind, plan.id, artworkKind).Scan(&oldLocal, &fallbackObject, &fallbackType, &fallbackValue)
			if rowErr == sql.ErrNoRows {
				fallbackValue = asset.fallbackValue
				if fallbackValue != "" {
					_ = tx.QueryRow(`SELECT object_name,content_type FROM catalog_artwork WHERE catalog_id=? AND kind=?`, plan.id, artworkKind).Scan(&fallbackObject, &fallbackType)
				}
			} else if rowErr != nil {
				err = rowErr
				return
			}
			name, previousObject, replaceErr := c.replaceArtwork(tx, plan.id, artworkKind, asset.artwork)
			if replaceErr != nil {
				err = replaceErr
				return
			}
			created = append(created, name)
			if oldLocal != "" {
				obsolete = append(obsolete, oldLocal)
			} else if fallbackObject == "" {
				obsolete = append(obsolete, previousObject)
			}
			if _, err = tx.Exec(`INSERT INTO catalog_local_artwork(catalog_kind,catalog_id,artwork_kind,location_id,relative_path,fingerprint,local_object_name,fallback_object_name,fallback_content_type,fallback_value) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(catalog_kind,catalog_id,artwork_kind) DO UPDATE SET location_id=excluded.location_id,relative_path=excluded.relative_path,fingerprint=excluded.fingerprint,local_object_name=excluded.local_object_name`, plan.kind, plan.id, artworkKind, plan.locationID, asset.source, asset.fingerprint, name, fallbackObject, fallbackType, fallbackValue); err != nil {
				return
			}
			if _, err = tx.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES(?,?,?,?,'local',0) ON CONFLICT(catalog_kind,catalog_id,field) DO UPDATE SET value=CASE WHEN locked=0 THEN excluded.value ELSE value END,source=CASE WHEN locked=0 THEN excluded.source ELSE source END`, plan.kind, plan.id, artworkKind, artworkURL(plan.id, artworkKind)); err != nil {
				return
			}
		}
	}
	return
}

func restoreLocalFieldTx(tx *sql.Tx, kind, id, field, value string, provider bool) error {
	if !provider {
		_, err := tx.Exec(`DELETE FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? AND field=? AND locked=0`, kind, id, field)
		return err
	}
	_, err := tx.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES(?,?,?,?,'provider',0) ON CONFLICT(catalog_kind,catalog_id,field) DO UPDATE SET value=CASE WHEN locked=0 THEN excluded.value ELSE value END,source=CASE WHEN locked=0 THEN excluded.source ELSE source END`, kind, id, field, value)
	return err
}
