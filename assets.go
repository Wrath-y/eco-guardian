// Package ecoguardian exposes immutable application assets for the production
// binary. Development serves Vite separately and never changes these bytes.
package ecoguardian

import "embed"

// Assets contains the OpenAPI contract, database migration, compiled v1
// schemas, and the web distribution that is produced by `make build`.
//
//go:embed api/*.yaml api/*.json migrations/*.sql internal/domain/assets/*.json web/dist/*
var Assets embed.FS
