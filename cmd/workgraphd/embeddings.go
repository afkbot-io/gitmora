package main

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/retrieval/ollama"
)

func embeddingClient(ctx context.Context, pool *pgxpool.Pool) (*ollama.Client, error) {
	endpoint := os.Getenv("WORKGRAPH_EMBEDDING_URL")
	queryEndpoint := os.Getenv("WORKGRAPH_EMBEDDING_QUERY_URL")
	model := os.Getenv("WORKGRAPH_EMBEDDING_MODEL")
	revision := os.Getenv("WORKGRAPH_EMBEDDING_REVISION")
	dims := os.Getenv("WORKGRAPH_EMBEDDING_DIMENSIONS")
	queryPrefix, passagePrefix := os.Getenv("WORKGRAPH_EMBEDDING_QUERY_PREFIX"), os.Getenv("WORKGRAPH_EMBEDDING_PASSAGE_PREFIX")
	if endpoint == "" {
		if queryEndpoint != "" || model != "" || revision != "" || dims != "" || queryPrefix != "" || passagePrefix != "" {
			return nil, errors.New("embedding settings require WORKGRAPH_EMBEDDING_URL")
		}
		return nil, nil
	}
	dimensions, err := strconv.Atoi(dims)
	if err != nil {
		return nil, errors.New("WORKGRAPH_EMBEDDING_DIMENSIONS must match the model")
	}
	client, err := ollama.New(ollama.Config{URL: endpoint, QueryURL: queryEndpoint, Model: model, Revision: revision, Dimensions: dimensions, QueryPrefix: queryPrefix, PassagePrefix: passagePrefix})
	if err != nil {
		return nil, errors.New("invalid local embedding configuration; verify URL, model, revision digest and dimensions")
	}
	if err = postgres.NewRetrievalStore(pool).VerifyEmbeddingProfile(ctx, client.Profile(), dimensions); err != nil {
		return nil, err
	}
	return client, nil
}
