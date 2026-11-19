//go:build !unix

package engine

import "errors"

func (e *Engine) installLinuxPackage(appExe string, appPID int, then string) error {
	return errors.New("installing from the app isn't supported on this system yet")
}
