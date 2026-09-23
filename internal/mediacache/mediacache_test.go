// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package mediacache // import "miniflux.app/v2/internal/mediacache"

import (
	"os"
	"strings"
	"testing"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
)

// configureMediaProxy initializes config.Opts with the given proxy mode and
// resource types, mirroring how the mediaproxy package tests set up config.
func configureMediaProxy(t *testing.T, mode, resourceTypes string) {
	t.Helper()

	os.Clearenv()
	os.Setenv("MEDIA_PROXY_MODE", mode)
	os.Setenv("MEDIA_PROXY_RESOURCE_TYPES", resourceTypes)
	os.Setenv("MEDIA_PROXY_PRIVATE_KEY", "test")

	parser := config.NewConfigParser()
	opts, err := parser.ParseEnvironmentVariables()
	if err != nil {
		t.Fatalf(`Config parsing failure: %v`, err)
	}
	config.Opts = opts
}

func TestExtractMediaURLs(t *testing.T) {
	// "all" mode proxifies both http and https for every enabled resource type.
	configureMediaProxy(t, "all", "image,video,audio")

	entry := &model.Entry{
		Content: `
			<img src="https://example.org/a.png">
			<img srcset="https://example.org/b-1x.png 1x, https://example.org/b-2x.png 2x">
			<picture><source srcset="https://example.org/c.webp"></picture>
			<video src="https://example.org/v.mp4" poster="https://example.org/p.jpg"></video>
			<audio><source src="https://example.org/song.mp3"></audio>
			<img src="data:image/png;base64,AAAA">
			<img src="/relative/ignored.png">
			<img src="https://example.org/a.png">
		`,
		Enclosures: model.EnclosureList{
			{URL: "https://example.org/podcast.mp3", MimeType: "audio/mpeg"},
			{URL: "https://example.org/a.png", MimeType: "image/png"},
		},
	}

	refs := ExtractMediaURLs(entry)

	got := make(map[string]string)
	for _, ref := range refs {
		got[ref.url] = ref.mediaType
	}

	expected := map[string]string{
		"https://example.org/a.png":       "image",
		"https://example.org/b-1x.png":    "image",
		"https://example.org/b-2x.png":    "image",
		"https://example.org/c.webp":      "image",
		"https://example.org/v.mp4":       "video",
		"https://example.org/p.jpg":       "image",
		"https://example.org/song.mp3":    "audio",
		"https://example.org/podcast.mp3": "audio",
	}

	if len(refs) != len(expected) {
		t.Fatalf("expected %d unique refs, got %d: %v", len(expected), len(refs), got)
	}

	for url, mediaType := range expected {
		actualType, ok := got[url]
		if !ok {
			t.Errorf("missing expected media URL %q", url)
			continue
		}
		if actualType != mediaType {
			t.Errorf("URL %q: expected type %q, got %q", url, mediaType, actualType)
		}
	}

	for url := range got {
		if _, ok := expected[url]; !ok {
			t.Errorf("unexpected media URL extracted: %q", url)
		}
	}
}

// TestExtractMediaURLsRespectsProxyConfig verifies that only media the proxy
// would actually serve gets cached, so cache and serving never diverge.
func TestExtractMediaURLsRespectsProxyConfig(t *testing.T) {
	// http-only mode with images only: https URLs and video/audio are skipped.
	configureMediaProxy(t, "http-only", "image")

	entry := &model.Entry{
		Content: `
			<img src="http://example.org/plain.png">
			<img src="https://example.org/secure.png">
			<video src="http://example.org/clip.mp4"></video>
		`,
		Enclosures: model.EnclosureList{
			{URL: "http://example.org/podcast.mp3", MimeType: "audio/mpeg"},
		},
	}

	refs := ExtractMediaURLs(entry)
	if len(refs) != 1 {
		t.Fatalf("expected only the http image to be cached, got %d refs: %+v", len(refs), refs)
	}
	if refs[0].url != "http://example.org/plain.png" || refs[0].mediaType != "image" {
		t.Fatalf("unexpected ref: %+v", refs[0])
	}
}

// TestExtractMediaURLsProxyDisabled verifies nothing is cached when the media
// proxy is off, because such media would never be served from the cache.
func TestExtractMediaURLsProxyDisabled(t *testing.T) {
	configureMediaProxy(t, "none", "image,video,audio")

	entry := &model.Entry{
		Content: `<img src="https://example.org/a.png">`,
	}
	if refs := ExtractMediaURLs(entry); len(refs) != 0 {
		t.Fatalf("expected no refs when proxy is disabled, got %+v", refs)
	}
}

func TestRelativePathLayout(t *testing.T) {
	const mediaURL = "https://example.org/media/movie.MP4"

	hash := URLHash(mediaURL)
	if hash == "" {
		t.Fatal("expected a non-empty url hash")
	}

	expected := "video/" + hash[:2] + "/" + hash + ".mp4"
	if actual := RelativePath(hash, "video", mediaURL); actual != expected {
		t.Fatalf("expected relative path %q, got %q", expected, actual)
	}

	if actual := RelativePath(hash, "", mediaURL); !strings.HasPrefix(actual, "other/"+hash[:2]+"/") {
		t.Fatalf("expected unknown media types under other/, got %q", actual)
	}

	if URLHash(mediaURL) != hash {
		t.Fatal("URLHash must be deterministic")
	}
}

func TestFileExtension(t *testing.T) {
	tests := []struct {
		name     string
		mediaURL string
		expected string
	}{
		{"plain", "https://example.org/a.png", ".png"},
		{"uppercased normalised", "https://example.org/A.MP4", ".mp4"},
		{"query string ignored", "https://example.org/a.jpg?w=800", ".jpg"},
		{"nested path", "https://example.org/x/y/z.jpeg", ".jpeg"},
		{"no extension", "https://example.org/image", ""},
		{"host only", "https://example.org", ""},
		{"too long rejected", "https://example.org/a.abcdef", ""},
		{"dot dot rejected", "https://example.org/a..", ""},
		{"non alphanumeric rejected", "https://example.org/a.ps'g", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if actual := FileExtension(tc.mediaURL); actual != tc.expected {
				t.Errorf("expected extension %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestRelativePathNeverEscapesCacheDirectory(t *testing.T) {
	const cacheDir = "/var/cache/miniflux"

	for _, mediaURL := range []string{
		"https://example.org/a..",
		"https://example.org/../../etc/passwd",
		"https://example.org/a.png/..%2f..%2fsecret",
		"https://example.org/x.%2e%2e/png",
	} {
		relativePath := RelativePath(URLHash(mediaURL), "image", mediaURL)
		fullPath, err := joinCachePath(cacheDir, relativePath)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", mediaURL, err)
		}
		if !strings.HasPrefix(fullPath, cacheDir+"/") {
			t.Fatalf("media URL %q escaped the cache directory: %q", mediaURL, fullPath)
		}
	}
}

func TestJoinCachePathRejectsTraversal(t *testing.T) {
	const cacheDir = "/var/cache/miniflux"

	// A traversal attempt must never resolve to a path outside the cache dir.
	// joinCachePath anchors the relative path so "../../etc/passwd" collapses to
	// a location still contained within the cache directory.
	fullPath, err := joinCachePath(cacheDir, "../../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(fullPath, cacheDir+"/") {
		t.Fatalf("path traversal escaped the cache directory: %q", fullPath)
	}

	fullPath, err = joinCachePath(cacheDir, "ab/abcdef")
	if err != nil {
		t.Fatalf("unexpected error for a valid relative path: %v", err)
	}
	if fullPath != "/var/cache/miniflux/ab/abcdef" {
		t.Fatalf("unexpected joined path: %q", fullPath)
	}
}
