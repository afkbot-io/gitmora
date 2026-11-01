package application

import (
	"sort"
	"workgraph.local/platform/internal/collector/domain"
)

// Journal hashes are identities, never chronology. Order known endpoints by
// verified local storage position so restart and manual application agree.
func orderFrontiers(tx Transaction, entries []domain.Frontier) error {
	segments := map[string]int64{}
	for _, f := range entries {
		if f.TargetSessionID == "" || f.MessageEnd == nil {
			continue
		}
		if _, ok := segments[f.TargetSessionID]; ok {
			continue
		}
		e, found, err := tx.FindSession(f.TargetSessionID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrConflict
		}
		segment := int64(1)
		if c := e.Recording.State().Snapshot.Continuation; c != nil {
			segment = c.Segment
		}
		segments[f.TargetSessionID] = segment
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.NativeKey != b.NativeKey {
			return a.NativeKey < b.NativeKey
		}
		as, bs := segments[a.TargetSessionID], segments[b.TargetSessionID]
		if as != bs {
			return as < bs
		}
		if a.MessageEnd != nil && b.MessageEnd != nil && *a.MessageEnd != *b.MessageEnd {
			return *a.MessageEnd < *b.MessageEnd
		}
		return a.ID < b.ID
	})
	return nil
}
