// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package model // import "miniflux.app/v2/internal/model"

// MediaCacheItem represents a media file cached on disk for a starred entry.
//
// Several entries can reference the same remote media URL. The physical file
// is stored once, keyed by URLHash, and each referencing entry owns a row. The
// file is removed from disk only when the last row referencing it is deleted.
type MediaCacheItem struct {
	ID        int64  `json:"id"`
	EntryID   int64  `json:"entry_id"`
	URLHash   string `json:"url_hash"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	MimeType  string `json:"mime_type"`
	Size      int64  `json:"size"`
}

// MediaCacheItemList represents a list of cached media files.
type MediaCacheItemList []*MediaCacheItem
