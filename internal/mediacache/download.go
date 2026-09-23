// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package mediacache // import "miniflux.app/v2/internal/mediacache"

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/fetcher"
	"miniflux.app/v2/internal/storage"
)

// CacheEntry downloads every media file referenced by the entry and records them
// in the database. It is safe to call when the cache is disabled (no-op).
func CacheEntry(store *storage.Storage, entry *model.Entry) {
	if !Enabled() || entry == nil {
		return
	}

	cacheDir := config.Opts.MediaCacheDirectory()
	maxSize := MaxFileSizeBytes()

	refs := ExtractMediaURLs(entry)
	if len(refs) == 0 {
		return
	}

	items := make(model.MediaCacheItemList, 0, len(refs))
	for _, ref := range refs {
		item, err := downloadMedia(cacheDir, entry.ID, ref, maxSize)
		if err != nil {
			slog.Warn("MediaCache: unable to cache media",
				slog.Int64("entry_id", entry.ID),
				slog.String("media_url", ref.url),
				slog.Any("error", err),
			)
			continue
		}
		if item != nil {
			items = append(items, item)
		}
	}

	if err := store.SaveMediaCacheItems(items); err != nil {
		slog.Error("MediaCache: unable to persist media cache rows",
			slog.Int64("entry_id", entry.ID),
			slog.Any("error", err),
		)
	}
}

// RemoveEntry deletes the cached media files owned by the entry. Files still
// referenced by other entries are preserved. It is a no-op when disabled.
func RemoveEntry(store *storage.Storage, entryID int64) {
	if !Enabled() {
		return
	}

	orphanedPaths, err := store.DeleteMediaCacheForEntry(entryID)
	if err != nil {
		slog.Error("MediaCache: unable to remove media cache rows",
			slog.Int64("entry_id", entryID),
			slog.Any("error", err),
		)
		return
	}

	cacheDir := config.Opts.MediaCacheDirectory()
	for _, relativePath := range orphanedPaths {
		fullPath, err := joinCachePath(cacheDir, relativePath)
		if err != nil {
			slog.Warn("MediaCache: "+err.Error())
			continue
		}
		if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("MediaCache: unable to delete cached file",
				slog.String("path", fullPath),
				slog.Any("error", err),
			)
		}
	}
}

// downloadMedia fetches a single media URL and writes it to the cache directory.
// When the file is already present on disk it is reused. It returns nil when the
// resource is skipped (too large or download failure is handled by the caller).
func downloadMedia(cacheDir string, entryID int64, ref mediaRef, maxSize int64) (*model.MediaCacheItem, error) {
	urlHash := URLHash(ref.url)
	relativePath := RelativePath(urlHash)

	fullPath, err := joinCachePath(cacheDir, relativePath)
	if err != nil {
		return nil, err
	}

	// Reuse an existing file downloaded for another entry.
	if info, statErr := os.Stat(fullPath); statErr == nil && info.Size() > 0 {
		return &model.MediaCacheItem{
			EntryID:   entryID,
			URLHash:   urlHash,
			Path:      filepath.ToSlash(relativePath),
			MediaType: ref.mediaType,
			MimeType:  detectMimeType(fullPath, ""),
			Size:      info.Size(),
		}, nil
	}

	requestBuilder := fetcher.NewRequestBuilder().
		WithTimeout(config.Opts.MediaProxyHTTPClientTimeout()).
		WithoutCompression()

	resp, err := requestBuilder.ExecuteRequest(ref.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// The cache downloader never sends a Range header, so a partial response
	// would indicate a truncated/unexpected file that must not be cached.
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("unexpected status code " + resp.Status)
	}

	if maxSize > 0 {
		if contentLength := resp.ContentLength; contentLength > maxSize {
			return nil, errors.New("media file exceeds the configured maximum size")
		}
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(fullPath), ".download-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	contentType := resp.Header.Get("Content-Type")
	written, err := io.Copy(tmpFile, io.LimitReader(resp.Body, maxSizeOrUnlimited(maxSize)))
	if err != nil {
		return nil, err
	}
	if maxSize > 0 && written > maxSize {
		return nil, errors.New("media file exceeds the configured maximum size")
	}

	if err := tmpFile.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		return nil, err
	}

	return &model.MediaCacheItem{
		EntryID:   entryID,
		URLHash:   urlHash,
		Path:      filepath.ToSlash(relativePath),
		MediaType: ref.mediaType,
		MimeType:  detectMimeType(fullPath, contentType),
		Size:      written,
	}, nil
}

func detectMimeType(fullPath, contentType string) string {
	if contentType != "" {
		if idx := strings.Index(contentType, ";"); idx >= 0 {
			contentType = contentType[:idx]
		}
		contentType = strings.TrimSpace(contentType)
		if contentType != "" && contentType != "application/octet-stream" {
			return contentType
		}
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return contentType
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}

// maxSizeOrUnlimited converts a byte limit into a value usable by io.LimitReader.
// A non-positive limit means "unlimited", represented by math.MaxInt64.
func maxSizeOrUnlimited(maxSize int64) int64 {
	if maxSize <= 0 {
		return math.MaxInt64
	}
	// Read one extra byte so we can detect bodies that exceed the limit.
	return maxSize + 1
}
