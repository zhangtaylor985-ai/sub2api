package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRcloneArchiveBackendUploadAndReadBack(t *testing.T) {
	if os.Getenv("SESSIONDELIVERY_RCLONE_HELPER") == "1" {
		runRcloneHelperProcess()
		return
	}

	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "session-delivery-20260810.tar.zst")
	require.NoError(t, os.WriteFile(source, []byte("archive-content"), 0o600))
	backend, err := NewRcloneArchiveBackend(RcloneArchiveConfig{
		Binary: os.Args[0],
		Remote: "gdrive:Sub2API/session-delivery",
	})
	require.NoError(t, err)
	backend.command = func(ctx context.Context, binary string, args ...string) *exec.Cmd {
		commandArgs := append([]string{"-test.run=TestRcloneArchiveBackendUploadAndReadBack", "--"}, args...)
		command := exec.CommandContext(ctx, binary, commandArgs...)
		command.Env = append(os.Environ(),
			"SESSIONDELIVERY_RCLONE_HELPER=1",
			"SESSIONDELIVERY_RCLONE_ROOT="+root,
		)
		return command
	}

	object, err := backend.Put(context.Background(), filepath.Base(source), source)
	require.NoError(t, err)
	require.Equal(t, backend.Name(), object.Backend)
	require.True(t, backend.Durable())
	require.NoError(t, backend.Verify(context.Background(), object))

	stored := helperObjectPath(root, object.Name)
	require.NoError(t, os.WriteFile(stored, []byte("corrupt"), 0o600))
	err = backend.Verify(context.Background(), object)
	require.ErrorContains(t, err, "read-back mismatch")
}

func TestRcloneArchiveBackendRejectsUnsafeRemote(t *testing.T) {
	_, err := NewRcloneArchiveBackend(RcloneArchiveConfig{Binary: os.Args[0], Remote: "relative/path"})
	require.ErrorContains(t, err, "named remote prefix")
}

func runRcloneHelperProcess() {
	separator := -1
	for index, value := range os.Args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	args := os.Args[separator+1:]
	root := os.Getenv("SESSIONDELIVERY_RCLONE_ROOT")
	switch args[0] {
	case "copyto":
		if len(args) < 3 {
			os.Exit(2)
		}
		source := args[len(args)-2]
		destination := helperObjectPath(root, args[len(args)-1])
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			os.Exit(3)
		}
		input, err := os.Open(source)
		if err != nil {
			os.Exit(4)
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if existing, readErr := os.ReadFile(destination); readErr == nil {
				incoming, sourceErr := os.ReadFile(source)
				if sourceErr == nil && string(existing) == string(incoming) {
					os.Exit(0)
				}
			}
			os.Exit(5)
		}
		if _, err := io.Copy(output, input); err != nil {
			_ = output.Close()
			os.Exit(6)
		}
		if err := output.Close(); err != nil {
			os.Exit(7)
		}
		os.Exit(0)
	case "cat":
		if len(args) != 2 {
			os.Exit(2)
		}
		file, err := os.Open(helperObjectPath(root, args[1]))
		if err != nil {
			os.Exit(8)
		}
		defer file.Close()
		if _, err := io.Copy(os.Stdout, file); err != nil {
			os.Exit(9)
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func helperObjectPath(root, remotePath string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(remotePath)))
	return filepath.Join(root, hex.EncodeToString(digest[:])+".object")
}
