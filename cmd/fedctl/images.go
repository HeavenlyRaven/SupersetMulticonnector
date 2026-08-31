package main

// SupersetBaseImage and ClickHouseBaseImage are the exact tag+digest pins
// used in compose.yaml and images/*/Dockerfile — kept here too so
// `fedctl preflight` can verify both platform manifests still exist
// without parsing YAML. If you re-pin the images, update both places.
//
// Resolved 2026-08-31 against Docker Hub; both confirmed to publish
// linux/amd64 and linux/arm64 manifests for this exact digest. Re-verify
// with `docker buildx imagetools inspect <ref>` before trusting an old
// pin — image tags can be rebuilt under the same version string (see
// README's version table for details).
const (
	SupersetBaseImage   = "apache/superset:6.1.0@sha256:59cd4af66006fe4cc98906eda42a771dbefdacb432f9ab083e02cdc6ff01f29d"
	ClickHouseBaseImage = "clickhouse/clickhouse-server:26.8.1.2041@sha256:9f8fa0d51f86611fda226c4085cdafdddce35312a60c934de2e4450d61fcb99a"
)
