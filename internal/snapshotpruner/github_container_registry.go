package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const answerSizeLimit = 8 << 20

// githubContainerRegistry is one GitHub Container Registry package of a personal account: its versions through GitHub's API, and its image indexes through the registry.
type githubContainerRegistry struct {
	client      *http.Client
	apiURL      string
	registryURL string
	owner       string
	packageName string
	token       string
}

type versionAnswer struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Metadata  struct {
		Container struct {
			Tags []string `json:"tags"`
		} `json:"container"`
	} `json:"metadata"`
}

func (registry githubContainerRegistry) versionsURL() string {
	return fmt.Sprintf("%s/users/%s/packages/container/%s/versions", registry.apiURL, url.PathEscape(registry.owner), url.PathEscape(registry.packageName))
}

// Versions are every version of the package, read a page at a time.
func (registry githubContainerRegistry) Versions(ctx context.Context) ([]packageVersion, error) {
	var versions []packageVersion
	for page := 1; ; page++ {
		var answers []versionAnswer
		pageURL := fmt.Sprintf("%s?per_page=100&page=%d", registry.versionsURL(), page)
		if err := registry.getJSON(ctx, pageURL, "Bearer "+registry.token, "application/vnd.github+json", &answers); err != nil {
			return nil, fmt.Errorf("failed to list the package's versions: %w", err)
		}
		for _, answer := range answers {
			versions = append(versions, packageVersion{
				ID: answer.ID, Digest: answer.Name, Tags: answer.Metadata.Container.Tags, CreatedAt: answer.CreatedAt,
			})
		}
		if len(answers) < 100 {
			return versions, nil
		}
	}
}

// IndexPlatformDigests are the digests of the platform images an image index lists; none for a version that is no index.
func (registry githubContainerRegistry) IndexPlatformDigests(ctx context.Context, digest string) ([]string, error) {
	var token struct {
		Token string `json:"token"`
	}
	tokenURL := fmt.Sprintf("%s/token?service=ghcr.io&scope=repository:%s/%s:pull", registry.registryURL, registry.owner, registry.packageName)
	if err := registry.getJSON(ctx, tokenURL, "Bearer "+registry.token, "application/json", &token); err != nil {
		return nil, fmt.Errorf("failed to authenticate to the registry: %w", err)
	}
	var index struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	manifestURL := fmt.Sprintf("%s/v2/%s/%s/manifests/%s", registry.registryURL, registry.owner, registry.packageName, digest)
	accepted := "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json"
	if err := registry.getJSON(ctx, manifestURL, "Bearer "+token.Token, accepted, &index); err != nil {
		return nil, err
	}
	var digests []string
	for _, manifest := range index.Manifests {
		digests = append(digests, manifest.Digest)
	}
	return digests, nil
}

// DeleteVersion deletes one version of the package.
func (registry githubContainerRegistry) DeleteVersion(ctx context.Context, id int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", registry.versionsURL(), id), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+registry.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := registry.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s answered %d", request.URL, response.StatusCode)
	}
	return nil
}

func (registry githubContainerRegistry) getJSON(ctx context.Context, address, authorization, accept string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Accept", accept)
	response, err := registry.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", request.URL, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, answerSizeLimit)).Decode(into)
}
