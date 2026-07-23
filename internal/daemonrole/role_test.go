package daemonrole

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionRole(t *testing.T) {
	tests := []struct {
		name        string
		build       string
		roleVersion string
		createRole  bool
		roleSymlink bool
		wantSymlink bool
		wantCurrent bool
	}{
		{
			name:        "same-version regular file is re-aliased",
			build:       "v1.0.0",
			roleVersion: "v1.0.0",
			createRole:  true,
			wantSymlink: true,
			wantCurrent: true,
		},
		{
			name:        "same-version symlink is kept",
			build:       "v1.0.0",
			roleVersion: "v1.0.0",
			createRole:  true,
			roleSymlink: true,
			wantSymlink: true,
		},
		{
			name:        "strictly-newer regular file is kept",
			build:       "v1.0.0",
			roleVersion: "v1.1.0",
			createRole:  true,
		},
		{
			name:        "missing role path is created",
			build:       "v1.0.0",
			wantSymlink: true,
			wantCurrent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			currentExecutable := filepath.Join(dir, "current")
			writeExecutable(t, currentExecutable, "v1.0.0")
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatalf("filepath.EvalSymlinks() error = %v", err)
			}

			rolePath := filepath.Join(dir, "role")
			otherExecutable := filepath.Join(dir, "other")
			if tt.createRole {
				if tt.roleSymlink {
					writeExecutable(t, otherExecutable, tt.roleVersion)
					if err := os.Symlink(otherExecutable, rolePath); err != nil {
						t.Fatalf("os.Symlink() error = %v", err)
					}
				} else {
					writeExecutable(t, rolePath, tt.roleVersion)
				}
			}

			resolvedCurrent := filepath.Join(resolvedDir, "current")
			if err := provisionRole(rolePath, resolvedCurrent, tt.build); err != nil {
				t.Fatalf("provisionRole() error = %v", err)
			}

			info, err := os.Lstat(rolePath)
			if err != nil {
				t.Fatalf("os.Lstat() error = %v", err)
			}
			gotSymlink := info.Mode()&os.ModeSymlink != 0
			if gotSymlink != tt.wantSymlink {
				t.Fatalf("rolePath symlink = %v, want %v", gotSymlink, tt.wantSymlink)
			}

			gotTarget, err := filepath.EvalSymlinks(rolePath)
			if err != nil {
				t.Fatalf("filepath.EvalSymlinks() error = %v", err)
			}
			wantTarget := filepath.Join(resolvedDir, "role")
			if tt.wantSymlink {
				wantTarget = filepath.Join(resolvedDir, "other")
				if tt.wantCurrent {
					wantTarget = resolvedCurrent
				}
			}
			if gotTarget != wantTarget {
				t.Fatalf("rolePath target = %q, want %q", gotTarget, wantTarget)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, version string) {
	t.Helper()
	script := "#!/bin/sh\necho " + version + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
