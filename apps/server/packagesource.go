// packagesource.go adapts the registry store to the agentapi.PackageSource
// interface, so the /agent/packages/{id} handler can serve a version's
// archive without agentapi importing internal/registry. It resolves a
// version id to its on-disk .tgz and opens it for streaming.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/yeluonight/skillfleet/internal/agentapi"
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
