// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package mediacache downloads and stores the media files (images, videos and
// audio) referenced by starred entries on the local filesystem, so they remain
// available even when the original remote links stop working.
package mediacache // import "miniflux.app/v2/internal/mediacache"

import (
	"fmt"
	"path"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/crypto"
	"miniflux.app/v2/internal/mediaproxy"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/sanitizer"

	"github.com/PuerkitoBio/goquery"
)

// Enabled reports whether the on-disk media cache is configured.
func Enabled() bool {
	return strings.TrimSpace(config.Opts.MediaCacheDirectory()) != ""
}

// mediaRef is a remote media resource extracted from an entry.
type mediaRef struct {
	url       string
	mediaType string // "image", "video" or "audio"
}

// URLHash returns the deterministic cache key for an absolute media URL.
// The media proxy handler uses the same function to look files up.
func URLHash(mediaURL string) string {
	return crypto.SHA256(mediaURL)
}

// RelativePath returns the on-disk path (relative to the cache directory) for a
// given url hash. Files are sharded across sub-directories using the first two
// hexadecimal characters of the hash to avoid huge flat directories.
func RelativePath(urlHash string) string {
	if len(urlHash) < 2 {
		return urlHash
	}
	return path.Join(urlHash[:2], urlHash)
}

// ExtractMediaURLs returns the media URLs referenced by an entry that will
// actually be served through the media proxy: images, inline videos and audios
// found in the HTML content, plus the enclosure attachments.
//
// The decision mirrors the media proxy rewriter exactly (same proxy mode and
// resource types), so we never download a file that the proxy would not serve.
// As a consequence, caching requires the media proxy to be enabled
// (MEDIA_PROXY_MODE different from "none").
func ExtractMediaURLs(entry *model.Entry) []mediaRef {
	if entry == nil {
		return nil
	}

	proxyMode := config.Opts.MediaProxyMode()
	proxyResourceTypes := config.Opts.MediaProxyResourceTypes()

	seen := make(map[string]bool)
	refs := make([]mediaRef, 0)

	// add handles inline content. mediaType is "image", "video" or "audio"; a
	// synthetic "<type>/" MIME prefix reuses the proxy's own type check.
	add := func(rawURL, mediaType string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || seen[rawURL] {
			return
		}
		if !mediaproxy.ShouldProxifyURLWithMimeType(rawURL, mediaType+"/", proxyMode, proxyResourceTypes) {
			return
		}
		seen[rawURL] = true
		refs = append(refs, mediaRef{url: rawURL, mediaType: mediaType})
	}

	extractFromContent(entry.Content, add)

	// Enclosures are proxified based on their real MIME type, exactly like
	// model.Enclosure.ProxifyEnclosureURL does.
	for _, enclosure := range entry.Enclosures {
		if enclosure == nil {
			continue
		}
		enclosureURL := strings.TrimSpace(enclosure.URL)
		if enclosureURL == "" || seen[enclosureURL] {
			continue
		}
		if !mediaproxy.ShouldProxifyURLWithMimeType(enclosureURL, enclosure.MimeType, proxyMode, proxyResourceTypes) {
			continue
		}
		seen[enclosureURL] = true
		refs = append(refs, mediaRef{url: enclosureURL, mediaType: enclosureMediaType(enclosure)})
	}

	return refs
}

func extractFromContent(htmlDocument string, add func(rawURL, mediaType string)) {
	if strings.TrimSpace(htmlDocument) == "" {
		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlDocument))
	if err != nil {
		return
	}

	doc.Find("img, picture source").Each(func(i int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			add(src, "image")
		}
		if srcset, ok := s.Attr("srcset"); ok {
			for _, candidate := range sanitizer.ParseSrcSetAttribute(srcset) {
				add(candidate.ImageURL, "image")
			}
		}
	})

	doc.Find("video, video source").Each(func(i int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			add(src, "video")
		}
		if poster, ok := s.Attr("poster"); ok {
			add(poster, "image")
		}
	})

	doc.Find("audio, audio source").Each(func(i int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			add(src, "audio")
		}
	})
}

func enclosureMediaType(enclosure *model.Enclosure) string {
	switch {
	case enclosure.IsImage():
		return "image"
	case enclosure.IsVideo():
		return "video"
	case enclosure.IsAudio():
		return "audio"
	default:
		return ""
	}
}

// MaxFileSizeBytes returns the configured per-file download limit, or 0 when
// unlimited.
func MaxFileSizeBytes() int64 {
	mb := config.Opts.MediaCacheMaxFileSizeMB()
	if mb <= 0 {
		return 0
	}
	return int64(mb) * 1024 * 1024
}

// SanitizeRelativePath guards against path traversal when joining a stored
// relative path with the cache directory.
func joinCachePath(cacheDir, relativePath string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(relativePath, "\\", "/"))
	full := path.Join(cacheDir, clean)
	if full != cacheDir && !strings.HasPrefix(full, strings.TrimSuffix(cacheDir, "/")+"/") {
		return "", fmt.Errorf("mediacache: refusing path outside cache directory: %q", relativePath)
	}
	return full, nil
}

// AbsolutePath resolves a stored relative cache path against the configured
// cache directory, guarding against path traversal. It returns ok=false when the
// cache is disabled or the path would escape the cache directory.
func AbsolutePath(relativePath string) (string, bool) {
	if !Enabled() {
		return "", false
	}
	fullPath, err := joinCachePath(config.Opts.MediaCacheDirectory(), relativePath)
	if err != nil {
		return "", false
	}
	return fullPath, true
}
