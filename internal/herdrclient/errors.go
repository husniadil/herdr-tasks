package herdrclient

import (
	"errors"
	"os/exec"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

func asCoded(err error, target **codes.Error) bool { return errors.As(err, target) }

func asExit(err error, target **exec.ExitError) bool { return errors.As(err, target) }
