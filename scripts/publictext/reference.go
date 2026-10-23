package main

import (
	"context"

	"github.com/hiono/taxion/internal/forge"
	"github.com/hiono/taxion/internal/publictext"
)

func resolveGitHub(ctx context.Context, target string) (publictext.Reference, error) {
	return forge.ResolveGitHubReference(ctx, target, nil)
}

func referenceMetadata(target, kind string, data []byte) (publictext.Reference, error) {
	return forge.ReferenceMetadata(target, kind, data)
}
