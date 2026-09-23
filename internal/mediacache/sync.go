// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package mediacache // import "miniflux.app/v2/internal/mediacache"

import (
	"log/slog"

	"miniflux.app/v2/internal/storage"
)

// SyncEntryStarredState caches the media of an entry that is currently starred,
// or removes its cached media when it is not starred. It reads the current
// starred flag from the database, so it can be called right after a toggle.
//
// It is a no-op when the media cache is disabled. This function performs network
// I/O and is expected to be called from a background goroutine.
func SyncEntryStarredState(store *storage.Storage, userID, entryID int64) {
	if !Enabled() {
		return
	}

	entry, err := store.NewEntryQueryBuilder(userID).WithEntryIDs(entryID).GetEntry()
	if err != nil {
		slog.Error("MediaCache: unable to fetch entry",
			slog.Int64("entry_id", entryID),
			slog.Any("error", err),
		)
		return
	}

	if entry == nil {
		return
	}

	if entry.Starred {
		CacheEntry(store, entry)
	} else {
		RemoveEntry(store, entryID)
	}
}
