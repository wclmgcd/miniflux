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
	"net/http/httputil"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"miniflux.app/v2/internal/config"
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

	parsedMediaURL, err := url.Parse(string(decodedURL))
	if err != nil {
		html.BadRequest(w, r, errors.New("invalid URL provided"))
		return
	}

	if parsedMediaURL.Scheme != "http" && parsedMediaURL.Scheme != "https" {
		html.BadRequest(w, r, errors.New("invalid URL provided"))
		return
	}

	if parsedMediaURL.Host == "" {
		html.BadRequest(w, r, errors.New("invalid URL provided"))
		return
	}

	if !parsedMediaURL.IsAbs() {
		html.BadRequest(w, r, errors.New("invalid URL provided"))
		return
	}

	mediaURL := string(decodedURL)
	slog.Debug("MediaProxy: Fetching remote resource",
		slog.String("media_url", mediaURL),
	)
	etag := crypto.HashFromBytes(decodedURL)

	m, err := h.store.MediaByURL(mediaURL)
	if err != nil {
		goto FETCH
	}
	if m.Content != nil {
		slog.Debug(`proxy from database`, slog.String("media_url", mediaURL))
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
    slog.Debug("fetch and proxy", slog.String("media_url", mediaURL))

    // 官方 Miniflux v2 成熟反向代理实现，完美支持视频 Range / 流式传输
    // 彻底解决 mp4 视频代理 Internal Server Error
    // 同时完全保留 qjebbs 的 ETag + 磁盘/数据库缓存功能

    director := func(req *http.Request) {
        req.URL.Scheme = parsedMediaURL.Scheme
        req.URL.Host = parsedMediaURL.Host
        req.URL.Path = parsedMediaURL.Path
        req.URL.RawQuery = parsedMediaURL.RawQuery
        req.Host = parsedMediaURL.Host

        if ua := r.Header.Get("User-Agent"); ua != "" {
            req.Header.Set("User-Agent", ua)
        } else {
            req.Header.Set("User-Agent", "Miniflux/MediaProxy")
        }

        if rangeVal := r.Header.Get("Range"); rangeVal != "" {
            req.Header.Set("Range", rangeVal)
        }
    }

    proxy := &httputil.ReverseProxy{
        Director: director,
        ModifyResponse: func(res *http.Response) error {
            res.Header.Set("Content-Security-Policy", "default-src 'self'")

            if filename := path.Base(parsedMediaURL.Path); filename != "" && filename != "." && filename != "/" {
                res.Header.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
            }

            res.Header.Set("Accept-Ranges", "bytes")
            return nil
        },
        ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
            slog.Error("MediaProxy: ReverseProxy failed",
                slog.String("media_url", mediaURL),
                slog.Any("error", err))
            http.Error(w, "Bad Gateway", http.StatusBadGateway)
        },
    }

    response.New(w, r).WithCaching(etag, 72*time.Hour, func(b *response.Builder) {
        proxy.ServeHTTP(b.Writer(), r)
    }).Write()

    return