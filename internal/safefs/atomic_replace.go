// safefs — atomic_replace.go: crash-safe replacement of a live install
// directory with a freshly staged one, via renames inside a single
// *os.Root (v1.0 §9.3 step 11).
//
// rename(2) is atomic within one filesystem: at no instant does an
// observer see a half-written skill directory. We exploit that with a
// two-rename dance, all relative to the same os.Root (hence same
// filesystem, since the staging dir was created inside the root):
//
//	BeginSwap:
//	  1. if the live dir exists, rename it aside to ".skillfleet-old-<r>"
//	  2. rename the staging dir into the live name
//	     - if step 2 fails, rename the aside dir back and return the error
//	       (the live install is untouched)
//	Commit:   RemoveAll the aside dir (the swap stuck; old bytes no longer needed)
//	Rollback: reverse step 2 then step 1, restoring the original live dir
//	          (used when a LATER step — e.g. the post-install rescan —
//	          fails and we must undo a swap that itself succeeded)
//
// The only window where a crash leaves an inconsistent on-disk state is
// between the two renames (live aside-d, staging not yet in place). That
// window is a single rename wide; a crash there leaves an obvious
// ".skillfleet-old-*" directory, and the persistent backup (backup.go)
// remains the authoritative recovery source. A boot-time reconciler for
// such residue is a future enhancement, out of this phase's scope.
package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// MarkerName is the fixed file name of the managed-install marker
// (v1.0 §9.2). Its leading dot makes fingerprint.Compute skip it, so a
// post-install rescan hashes only the skill's own content. Defined here
// (the replace layer) because both backup.go and the executor reference
// it as the single canonical name.
const MarkerName = ".skillfleet-install.json"

// oldAsidePrefix is the leading-dot prefix of the aside (set-aside old
// install) directory created during a swap. Leading dot → skipped by
// fingerprint, and obviously-temporary if a crash leaves it behind.
const oldAsidePrefix = ".skillfleet-old-"

// Errors returned by the swap layer.
var (
	ErrSwapStagingMissing = errors.New("safefs: staging dir missing for swap")
	ErrSwapAlreadyDone    = errors.New("safefs: swap already committed or rolled back")
)

// Swap holds the state of an in-progress atomic replace so it can be
// committed (finalise) or rolled back (undo). Construct with BeginSwap.
// A Swap is single-use: Commit or Rollback marks it done.
type Swap struct {
	root       *os.Root
	skillRel   string // the live install name (root-relative)
	stagingRel string // the staged tree to move into place
	asideRel   string // where the old live dir was moved ("" if none existed)
	hadOld     bool   // whether a live dir existed before the swap
	done       bool
}

// BeginSwap performs the forward swap: it moves any existing live
// directory aside and renames the staging directory into the live name.
// On success the staged tree is the live install and the old bytes (if
// any) sit at asideRel awaiting Commit (delete) or Rollback (restore).
// If the staging→live rename fails, the old dir is moved back and the
// error returned with the live install unchanged.
func BeginSwap(root *os.Root, skillRel, stagingRel string) (*Swap, error) {
	if skillRel == "" || skillRel == "." {
		return nil, fmt.Errorf("safefs: swap requires a named skill dir")
	}
	// Staging must exist; a missing staging dir is a caller bug.
	if _, err := root.Lstat(stagingRel); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSwapStagingMissing, stagingRel)
	}

	s := &Swap{root: root, skillRel: skillRel, stagingRel: stagingRel}

	// Does a live install already exist?
	if _, err := root.Lstat(skillRel); err == nil {
		s.hadOld = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("safefs: stat live dir: %w", err)
	}

	if s.hadOld {
		aside, err := randAside()
		if err != nil {
			return nil, err
		}
		s.asideRel = aside
		if err := root.Rename(skillRel, aside); err != nil {
			return nil, fmt.Errorf("safefs: move live aside: %w", err)
		}
	}

	// Move staging into the live name.
	if err := root.Rename(stagingRel, skillRel); err != nil {
		// Roll the aside dir back so the live install is untouched.
		if s.hadOld {
			if rbErr := root.Rename(s.asideRel, skillRel); rbErr != nil {
				// Both renames failed: surface a combined error. The aside
				// dir name is in the message for manual recovery.
				return nil, fmt.Errorf("safefs: swap failed (%v) AND aside-restore failed (%v); old install at %s", err, rbErr, s.asideRel)
			}
		}
		return nil, fmt.Errorf("safefs: move staging into place: %w", err)
	}
	return s, nil
}

// Commit finalises a successful swap by deleting the set-aside old
// directory. After Commit the swap is done and the old bytes are gone
// (the persistent backup remains for any later rollback). Idempotent
// guard: a second Commit/Rollback returns ErrSwapAlreadyDone.
func (s *Swap) Commit() error {
	if s.done {
		return ErrSwapAlreadyDone
	}
	s.done = true
	if s.hadOld {
		if err := s.root.RemoveAll(s.asideRel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("safefs: commit cleanup aside: %w", err)
		}
	}
	return nil
}

// Rollback undoes a completed swap: it moves the just-installed staging
// tree back to its staging name and restores the old directory to the
// live name. Used when a step AFTER BeginSwap (e.g. the rescan) fails.
// After Rollback the on-disk state matches the pre-swap state (the live
// install is the original; the staged tree is back at stagingRel for
// cleanup). Idempotent guard as for Commit.
func (s *Swap) Rollback() error {
	if s.done {
		return ErrSwapAlreadyDone
	}
	s.done = true

	// Move the newly-installed tree out of the live name, back to staging,
	// so the live name is free for the old dir to return.
	if err := s.root.Rename(s.skillRel, s.stagingRel); err != nil {
		return fmt.Errorf("safefs: rollback move new aside: %w", err)
	}
	if s.hadOld {
		if err := s.root.Rename(s.asideRel, s.skillRel); err != nil {
			return fmt.Errorf("safefs: rollback restore old: %w", err)
		}
	}
	return nil
}

// randAside returns a fresh, unguessable aside directory name.
func randAside() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("safefs: aside rand: %w", err)
	}
	return oldAsidePrefix + hex.EncodeToString(raw[:]), nil
}
