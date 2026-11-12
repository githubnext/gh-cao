// Package postgres supplies the reviewed PostgreSQL privilege contracts.
package postgres

import _ "embed"

//go:embed canonical-grants.sql
var CanonicalGrants string

//go:embed operational-grants.sql
var OperationalGrants string
