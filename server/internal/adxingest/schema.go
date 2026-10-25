package adxingest

import _ "embed"

//go:embed schema.gen.kql
var schema string

// Schema returns the operator-applied KQL generated from server/spec/adx.tsp.
func Schema() string { return schema }
