// Package api ships the OpenAPI document inside the binary so the served
// docs always match the deployed version.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPISpec []byte
