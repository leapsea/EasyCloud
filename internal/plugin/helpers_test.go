package plugin

import "github.com/easycloud/easycloud/internal/config"

func dbtestSQLite() config.Database { return config.Database{Driver: "sqlite"} }
