package server

import "github.com/easycloud/easycloud/internal/platform/errs"

func errorsNew404() error { return errs.ErrNotFound }
