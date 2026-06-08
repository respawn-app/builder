package metadata

import _ "embed"

//go:embed queries/set_project_key.sql
var setProjectKeyQuery string
