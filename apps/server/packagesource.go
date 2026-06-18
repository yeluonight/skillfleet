// packagesource.go adapts the registry store to the agentapi.PackageSource
// interface, so the /agent/packages/{id} handler can serve a version's
// archive without agentapi importing internal/registry. It resolves a
// version id to its on-disk .tgz and opens it for streaming.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentapi"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// registryPackageSource serves package archives out of the registry's
// package store. It implements agentapi.PackageSource.
type registryPackageSource struct {
	reg *registry.Store
}

// ArchiveForVersion resolves versionID to its archive file and opens it.
// An unknown version (or one without an archive on disk) maps to
// agentapi.ErrPackageNotFound so the handler returns 404; other errors
// propagate as 500.
func (s registryPackageSource) ArchiveForVersion(versionID string) (*os.File, int64, error) {
	v, err := s.reg.Get(context.Background(), versionID)
	if err != nil {
		if errors.Is(err, registry.ErrVersionNotFnd) {
			return nil, 0, agentapi.ErrPackageNotFound
		}
		return nil, 0, err
	}
	if v.PackagePath == "" {
		return nil, 0, agentapi.ErrPackageNotFound
	}
	path := s.reg.ArchivePath(v)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, agentapi.ErrPackageNotFound
		}
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// registryAdopter publishes agent-uploaded skill files as a new registry
// version. It implements agentapi.SkillAdopter, adapting the upload wire
// types to registry.PublishFromFiles so agentapi need not import registry.
type registryAdopter struct {
	reg *registry.Store
}

// AdoptSkill publishes the uploaded (already-decoded) file content as a new
// version under the given name (KindImport — the bytes came from an
// external/device source, like a zip import). A caller-fixable problem
// (empty name, unsafe path) maps to agentapi.ErrAdoptInvalid so the handler
// returns 400. Publishing identical content is idempotent at the registry
// layer, so re-adopting an unchanged skill is harmless.
func (a registryAdopter) AdoptSkill(name string, files []deploy.AdoptFile, source deploy.AdoptSource) (string, error) {
	inmem := make([]registry.InMemoryFile, 0, len(files))
	for _, f := range files {
		inmem = append(inmem, registry.InMemoryFile{Path: f.Path, Content: f.Content})
	}
	v, err := a.reg.PublishFromFiles(context.Background(), inmem, registry.PublishParams{
		Name: name,
		Kind: registry.KindImport,
	}, time.Now())
	if err != nil {
		// registry path/name validation errors are caller-fixable.
		if errors.Is(err, registry.ErrEmptyName) {
			return "", fmt.Errorf("%w: %v", agentapi.ErrAdoptInvalid, err)
		}
		return "", err
	}
	return v.ID, nil
}
