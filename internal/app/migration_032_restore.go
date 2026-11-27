package app

import _ "embed"

//go:embed testdata/032_restore.sql
var Migration032RestoreSQL string