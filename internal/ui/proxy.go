// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/crypto"
	"miniflux.app/v2/internal/filesystem"
	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/http/response/html"
	"miniflux.app/v2/internal/reader/media"
)

func (h *handler) mediaProxy(w http.ResponseWriter, r *http.Request) {
	// If we receive a "If-None-Match" header, we assume the media is already stored in browser cache.
	if r.Header.Get("If-None-Match") != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	encodedDigest := request.RouteStringParam(r, "encodedDigest")
	encodedURL := request.RouteStringParam(r, "encodedURL")
	if encodedURL == "" {
		html.BadRequest(w, r, errors.New("no URL provided"))
		return
	}

	decodedDigest, err := base64.URLEncoding.DecodeString(encodedDigest)
	if err != nil {
		html.BadRequest(w, r, errors.New("unable to decode this digest"))
		return
	}

	decodedURL, err := base64.URLEncoding.DecodeString(encodedURL)
	if err != nil {
		html.BadRequest(w, r, errors.New("unable to decode this URL"))
		return
	}

	mac := hmac.New(sha256.New, config.Opts.MediaProxyPrivateKey())
	mac.Write(decodedURL)
	expectedMAC := mac.Sum(nil)

	if !hmac.Equal(decodedDigest, expectedMAC) {
		html.Forbidden(w, r)
		return
	}

	mediaURL := string(decodedURL)

if mediaURL == "" {
	html.BadRequest(w, r, errors.New("invalid URL provided"))
	return
}

if !strings.HasPrefix(mediaURL, "http://") &&
	!strings.HasPrefix(mediaURL, "https://") {
	html.BadRequest(w, r, errors.New("invalid URL provided"))
	return
}

// ⚠️ 只用于后面取文件名，不作为校验
parsedMediaURL, _ := url.Parse(mediaURL)

slog.Debug("MediaProxy: Fetching remote resource",
	slog.String("media_url", mediaURL),
)

	etag := crypto.HashFromBytes(decodedURL)

	m, err := h.store.MediaByURL(mediaURL)
if err != nil || m == nil {
    // ★ 关键：确保 m 不为 nil，且 URL 有值
    m = &model.Media{
        URL: mediaURL,
    }
    goto FETCH
}

	if m.Content != nil {
	slog.Debug(`proxy from database`, slog.String("media_url", mediaURL))

	// ⚠️ 视频 / Range 请求：不要走缓存封装
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Type", m.MimeType)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		w.Write(m.Content)
		return
	}

	// 图片 / 普通资源才走缓存
	response.New(w, r).WithCaching(etag, 72*time.Hour, func(b *response.Builder) {
		b.WithHeader("Content-Type", m.MimeType)
		b.WithBody(m.Content)
		b.WithoutCompression()
		b.Write()
	})
	return
}


	if m.Cached {
		// cache is located in file system
		var file *os.File
		file, err = filesystem.MediaFileByHash(m.URLHash)
		if err != nil {
			slog.Debug("Unable to fetch media from file system: %s", err)
			goto FETCH
		}
		defer file.Close()
		slog.Debug(`proxy from filesystem`, slog.String("media_url", mediaURL))
		response.New(w, r).WithCaching(etag, 72*time.Hour, func(b *response.Builder) {
			b.WithHeader("Content-Type", m.MimeType)
			b.WithBody(file)
			b.WithoutCompression()
			b.Write()
		})
		return
	}

FETCH:
	// TODO: apply config
	// clt := &http.Client{
	// 	Transport: &http.Transport{
	// 		IdleConnTimeout: time.Duration(config.Opts.MediaProxyHTTPClientTimeout()) * time.Second,
	// 	},s
	// 	Timeout: time.Duration(config.Opts.MediaProxyHTTPClientTimeout()) * time.Second,
	// }
	slog.Debug(`fetch and proxy`, slog.String("media_url", mediaURL))
	if mediaURL == "" {
	slog.Warn("MediaProxy: Empty media URL before fetch")
	html.BadRequest(w, r, errors.New("invalid media URL"))
	return
}
	resp, err := media.FetchMedia(m, r)
	if err != nil {
		slog.Error("MediaProxy: Unable to initialize HTTP client",
			slog.String("media_url", mediaURL),
			slog.Any("error", err),
		)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		slog.Warn("MediaProxy: "+http.StatusText(http.StatusRequestedRangeNotSatisfiable),
			slog.String("media_url", mediaURL),
			slog.Int("status_code", resp.StatusCode),
		)
		html.RequestedRangeNotSatisfiable(w, r, resp.Header.Get("Content-Range"))
		return
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		slog.Warn("MediaProxy: Unexpected response status code",
			slog.String("media_url", mediaURL),
			slog.Int("status_code", resp.StatusCode),
		)

		// Forward the status code from the origin.
		http.Error(w, fmt.Sprintf("Origin status code is %d", resp.StatusCode), resp.StatusCode)
		return
	}

	response.New(w, r).WithCaching(etag, 72*time.Hour, func(b *response.Builder) {
		b.WithStatus(resp.StatusCode)
		b.WithHeader("Content-Security-Policy", `default-src 'self'`)
		b.WithHeader("Content-Type", resp.Header.Get("Content-Type"))

		if filename := path.Base(parsedMediaURL.Path); filename != "" {
			b.WithHeader("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
		}

		forwardedResponseHeader := []string{"Content-Encoding", "Content-Type", "Content-Length", "Accept-Ranges", "Content-Range"}
		for _, responseHeaderName := range forwardedResponseHeader {
			if resp.Header.Get(responseHeaderName) != "" {
				b.WithHeader(responseHeaderName, resp.Header.Get(responseHeaderName))
			}
		}
		b.WithBody(resp.Body)
		b.WithoutCompression()
		b.Write()
	})
}
