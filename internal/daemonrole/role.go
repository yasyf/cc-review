// Package daemonrole owns cc-review's stable daemon executable identity.
package daemonrole

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	dkrole "github.com/yasyf/daemonkit/daemonrole"
	dkversion "github.com/yasyf/daemonkit/version"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

const roleID = "com.yasyf.cc-review"

// Classifier returns cc-review's fixed daemon role after refreshing its alias.
func Classifier() dkrole.Classifier {
	role := dkrole.Classifier{
		RoleID: roleID, RolePath: filepath.Join(paths.StateDir(), "bin", "cc-review"),
	}
	if err := provision(role.RolePath); err != nil {
		panic(err)
	}
	return role
}

func provision(rolePath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	return provisionRole(rolePath, executable, version.Build())
}

func provisionRole(rolePath, executable, build string) error {
	if target, targetErr := filepath.EvalSymlinks(rolePath); targetErr == nil {
		currentVersion, versionErr := executableVersion(rolePath)
		if target == executable {
			return nil
		}
		if versionErr == nil {
			if dkversion.Newer(currentVersion, build) {
				return nil
			}
			if !dkversion.Newer(build, currentVersion) {
				info, err := os.Lstat(rolePath)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return nil
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(rolePath), ".cc-review-role-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	if err := os.Symlink(executable, tmpPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	return os.Rename(tmpPath, rolePath)
}

func executableVersion(path string) (string, error) {
	cmd := exec.Command(path, "--version") //nolint:gosec // role path is the exact local service identity
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return "", errors.New("cc-review daemon role version timed out")
	}
	return strings.TrimSpace(out.String()), nil
}
