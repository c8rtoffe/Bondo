package engine

import (
	"github.com/anacrolix/torrent"
)

// PieceStatusValue constants for visualization:
// 0 = Pending/Unrequested
// 1 = Partial/Downloading
// 2 = Complete/Verified
// 3 = Checking/Hashing
const (
	PieceStatusPending     = 0
	PieceStatusDownloading = 1
	PieceStatusComplete    = 2
	PieceStatusChecking    = 3
)

// ExtractPieceStates returns an array of status codes for all pieces of a torrent.
func ExtractPieceStates(t *torrent.Torrent) []int {
	numPieces := t.NumPieces()
	if numPieces <= 0 {
		return nil
	}

	states := make([]int, numPieces)
	for i := 0; i < numPieces; i++ {
		piece := t.Piece(i)
		state := piece.State()

		if state.Complete {
			states[i] = PieceStatusComplete
		} else if state.Hashing || state.Checking || state.QueuedForHash {
			states[i] = PieceStatusChecking
		} else if state.Partial {
			states[i] = PieceStatusDownloading
		} else {
			states[i] = PieceStatusPending
		}
	}

	return states
}

// PrioritizeInitialPieces boosts the priority of head pieces for fast file verification and start.
func PrioritizeInitialPieces(t *torrent.Torrent, count int) {
	numPieces := t.NumPieces()
	if count > numPieces {
		count = numPieces
	}

	for i := 0; i < count; i++ {
		t.Piece(i).SetPriority(torrent.PiecePriorityHigh)
	}
}

// CountCompletedPieces computes the total number of verified complete pieces.
func CountCompletedPieces(t *torrent.Torrent) int {
	runs := t.PieceStateRuns()
	completed := 0
	for _, r := range runs {
		if r.Complete {
			completed += r.Length
		}
	}
	return completed
}
