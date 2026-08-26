package packageinfo

//go:generate go run ./cmd/schema -out ../../api/package-manifest.schema.json

// SchemaDocument is the source of truth for the generated JSON Schema file.
func SchemaDocument() map[string]any {
	hash := map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}
	file := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"role", "path", "size_bytes", "sha256"},
		"properties": map[string]any{
			"role":       map[string]any{"type": "string", "pattern": "^[a-z0-9][a-z0-9._-]{0,127}$"},
			"path":       map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._/-]*$"},
			"size_bytes": map[string]any{"type": "integer", "minimum": 0},
			"sha256":     hash,
		},
	}
	component := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"kind", "id", "version", "files"},
		"properties": map[string]any{
			"kind":       map[string]any{"type": "string", "enum": []string{"local-rag", "python-runtime", "embedding-model", "rerank-model"}},
			"id":         map[string]any{"type": "string", "pattern": "^[a-z0-9][a-z0-9._-]{0,127}$"},
			"version":    map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"entrypoint": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._/-]*$"},
			"files":      map[string]any{"type": "array", "minItems": 1, "items": file},
		},
		"allOf": []any{
			map[string]any{"if": map[string]any{"properties": map[string]any{"kind": map[string]any{"enum": []string{"local-rag", "python-runtime"}}}, "required": []string{"kind"}}, "then": map[string]any{"required": []string{"entrypoint"}}},
			map[string]any{"if": map[string]any{"properties": map[string]any{"kind": map[string]any{"enum": []string{"embedding-model", "rerank-model"}}}, "required": []string{"kind"}}, "then": map[string]any{"not": map[string]any{"required": []string{"entrypoint"}}}},
		},
	}
	containsKind := func(kind string) map[string]any {
		return map[string]any{"contains": map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"const": kind}}, "required": []string{"kind"}}, "minContains": 1}
	}
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://eco-guardian.local/schemas/package-manifest-v1.json",
		"title":   "Eco Guardian Package Manifest v1",
		"type":    "object", "additionalProperties": false,
		"required": []string{"schema_version", "package_mode", "supported_platform", "eco_guardian", "embedded_asset_digests", "components"},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": 1},
			"package_mode":   map[string]any{"type": "string", "enum": []string{"complete", "lightweight"}},
			"supported_platform": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"os", "architecture"},
				"properties": map[string]any{"os": map[string]any{"const": "windows"}, "architecture": map[string]any{"const": "amd64"}},
			},
			"eco_guardian": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"version", "build", "commit", "runtime_status_schema_version", "executable", "size_bytes", "sha256"},
				"properties": map[string]any{
					"version": map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$"},
					"build":   map[string]any{"type": "string", "minLength": 1}, "commit": map[string]any{"type": "string", "minLength": 1},
					"runtime_status_schema_version": map[string]any{"type": "string", "minLength": 1},
					"executable":                    map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._/-]*$"},
					"size_bytes":                    map[string]any{"type": "integer", "minimum": 0},
					"sha256":                        hash,
				},
			},
			"embedded_asset_digests": map[string]any{"type": "object", "minProperties": 1, "additionalProperties": hash},
			"components":             map[string]any{"type": "array", "maxItems": 4, "items": component},
		},
		"allOf": []any{
			map[string]any{"if": map[string]any{"properties": map[string]any{"package_mode": map[string]any{"const": "lightweight"}}}, "then": map[string]any{"properties": map[string]any{"components": map[string]any{"maxItems": 0}}}},
			map[string]any{"if": map[string]any{"properties": map[string]any{"package_mode": map[string]any{"const": "complete"}}}, "then": map[string]any{"properties": map[string]any{"components": map[string]any{"minItems": 4, "maxItems": 4, "allOf": []any{containsKind("local-rag"), containsKind("python-runtime"), containsKind("embedding-model"), containsKind("rerank-model")}}}}},
		},
	}
}
