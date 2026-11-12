package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// snapshotTagMarker marks a snapshot's tag, such as `0.0.1-snapshot.20261111175512`.
const snapshotTagMarker = "-snapshot."

// packageVersion is one version of a container package: an image index or a platform image, its digest, its tags and when it was published.
type packageVersion struct {
	ID        int64
	Digest    string
	Tags      []string
	CreatedAt time.Time
}

// packageRegistry is where the package's versions are listed and deleted, and its image indexes read.
type packageRegistry interface {
	Versions(ctx context.Context) ([]packageVersion, error)
	IndexPlatformDigests(ctx context.Context, digest string) ([]string, error)
	DeleteVersion(ctx context.Context, id int64) error
}

func isExpiredSnapshot(version packageVersion, deadline time.Time) bool {
	if len(version.Tags) == 0 || !version.CreatedAt.Before(deadline) {
		return false
	}
	for _, tag := range version.Tags {
		if !strings.Contains(tag, snapshotTagMarker) {
			return false
		}
	}
	return true
}

// pruneSnapshots deletes every snapshot index published before the deadline and the platform images only it uses, answering the digests deleted.
func pruneSnapshots(ctx context.Context, registry packageRegistry, deadline time.Time, dryRun bool) ([]string, error) {
	versions, err := registry.Versions(ctx)
	if err != nil {
		return nil, err
	}
	byDigest := map[string]packageVersion{}
	for _, version := range versions {
		byDigest[version.Digest] = version
	}
	keptPlatforms := map[string]bool{}
	var expired []packageVersion
	expiredPlatforms := map[string]bool{}
	for _, version := range versions {
		if len(version.Tags) == 0 {
			continue
		}
		platforms, err := registry.IndexPlatformDigests(ctx, version.Digest)
		if err != nil {
			return nil, fmt.Errorf("failed to read image index %s: %w", version.Digest, err)
		}
		if !isExpiredSnapshot(version, deadline) {
			for _, platform := range platforms {
				keptPlatforms[platform] = true
			}
			continue
		}
		expired = append(expired, version)
		for _, platform := range platforms {
			expiredPlatforms[platform] = true
		}
	}
	var deleted []string
	deleteVersion := func(version packageVersion) error {
		if !dryRun {
			if err := registry.DeleteVersion(ctx, version.ID); err != nil {
				return fmt.Errorf("failed to delete %s: %w", version.Digest, err)
			}
		}
		deleted = append(deleted, version.Digest)
		return nil
	}
	for _, version := range expired {
		if err := deleteVersion(version); err != nil {
			return deleted, err
		}
	}
	for _, platform := range slices.Sorted(maps.Keys(expiredPlatforms)) {
		version, held := byDigest[platform]
		if !held || keptPlatforms[platform] {
			continue
		}
		if err := deleteVersion(version); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
