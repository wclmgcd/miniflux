// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

import (
	"database/sql"
	"fmt"

	"miniflux.app/v2/internal/model"
)

// SaveMediaCacheItems inserts cached media rows for an entry, ignoring rows
// that already exist for the same (entry_id, url_hash) pair.
func (s *Storage) SaveMediaCacheItems(items model.MediaCacheItemList) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf(`store: unable to start transaction: %v`, err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO entry_media_cache (entry_id, url_hash, path, media_type, mime_type, size)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (entry_id, url_hash) DO NOTHING
	`

	for _, item := range items {
		if _, err := tx.Exec(query, item.EntryID, item.URLHash, item.Path, item.MediaType, item.MimeType, item.Size); err != nil {
			return fmt.Errorf(`store: unable to save media cache item: %v`, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(`store: unable to commit transaction: %v`, err)
	}

	return nil
}

// MediaCacheItemsByEntryID returns all cached media rows for the given entry.
func (s *Storage) MediaCacheItemsByEntryID(entryID int64) (model.MediaCacheItemList, error) {
	query := `
		SELECT id, entry_id, url_hash, path, media_type, mime_type, size
		FROM entry_media_cache
		WHERE entry_id = $1
		ORDER BY id ASC
	`

	rows, err := s.db.Query(query, entryID)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch media cache rows: %v`, err)
	}
	defer rows.Close()

	items := make(model.MediaCacheItemList, 0)
	for rows.Next() {
		var item model.MediaCacheItem
		if err := rows.Scan(&item.ID, &item.EntryID, &item.URLHash, &item.Path, &item.MediaType, &item.MimeType, &item.Size); err != nil {
			return nil, fmt.Errorf(`store: unable to fetch media cache row: %v`, err)
		}
		items = append(items, &item)
	}

	return items, nil
}

// MediaCachePathByURLHash returns the on-disk path (relative to the cache
// directory) and MIME type of a cached media file. The boolean is false when
// the URL is not cached.
func (s *Storage) MediaCachePathByURLHash(urlHash string) (path string, mimeType string, ok bool) {
	query := `SELECT path, mime_type FROM entry_media_cache WHERE url_hash = $1 LIMIT 1`
	err := s.db.QueryRow(query, urlHash).Scan(&path, &mimeType)
	if err != nil {
		return "", "", false
	}
	return path, mimeType, true
}

// DeleteMediaCacheForEntry removes all cached media rows for the given entry and
// returns the on-disk paths that are no longer referenced by any other entry.
// The caller is responsible for deleting those files (reference counting).
func (s *Storage) DeleteMediaCacheForEntry(entryID int64) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf(`store: unable to start transaction: %v`, err)
	}
	defer tx.Rollback()

	// Snapshot the (url_hash, path) pairs owned by this entry before deleting.
	type hashPath struct{ urlHash, path string }
	var owned []hashPath

	rows, err := tx.Query(`SELECT url_hash, path FROM entry_media_cache WHERE entry_id = $1`, entryID)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch media cache rows: %v`, err)
	}
	for rows.Next() {
		var hp hashPath
		if err := rows.Scan(&hp.urlHash, &hp.path); err != nil {
			rows.Close()
			return nil, fmt.Errorf(`store: unable to fetch media cache row: %v`, err)
		}
		owned = append(owned, hp)
	}
	rows.Close()

	if len(owned) == 0 {
		// Nothing cached for this entry; commit the no-op transaction.
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf(`store: unable to commit transaction: %v`, err)
		}
		return nil, nil
	}

	if _, err := tx.Exec(`DELETE FROM entry_media_cache WHERE entry_id = $1`, entryID); err != nil {
		return nil, fmt.Errorf(`store: unable to delete media cache rows: %v`, err)
	}

	orphaned := make([]string, 0)
	seen := make(map[string]bool)
	for _, hp := range owned {
		if seen[hp.path] {
			continue
		}

		var stillReferenced int
		err := tx.QueryRow(`SELECT count(*) FROM entry_media_cache WHERE url_hash = $1`, hp.urlHash).Scan(&stillReferenced)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf(`store: unable to count media references: %v`, err)
		}

		if stillReferenced == 0 {
			orphaned = append(orphaned, hp.path)
		}
		seen[hp.path] = true
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(`store: unable to commit transaction: %v`, err)
	}

	return orphaned, nil
}
