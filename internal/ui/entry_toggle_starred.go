// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"net/http"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/mediacache"
)

func (h *handler) toggleStarred(w http.ResponseWriter, r *http.Request) {
	userID := request.UserID(r)
	entryID := request.RouteInt64Param(r, "entryID")
	if err := h.store.ToggleStarred(userID, entryID); err != nil {
		response.JSONServerError(w, r, err)
		return
	}

	go mediacache.SyncEntryStarredState(h.store, userID, entryID)

	response.JSON(w, r, "OK")
}
