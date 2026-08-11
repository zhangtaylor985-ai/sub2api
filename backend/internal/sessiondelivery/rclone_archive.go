package sessiondelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
)

const rcloneErrorOutputLimit = 4096

type RcloneArchiveConfig struct {
	Binary string
	Remote string
}

// RcloneArchiveBackend writes immutable, date-named archive objects to a
// configured rclone remote. A Google Drive or Google Shared Drive remote is
// the intended production target. Authentication stays in rclone's own
// protected configuration and is never passed through Session records.
type RcloneArchiveBackend struct {
	binary  string
	remote  string
	command func(context.Context, string, ...string) *exec.Cmd
}

func NewRcloneArchiveBackend(config RcloneArchiveConfig) (*RcloneArchiveBackend, error) {
	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = "rclone"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("locate rclone executable: %w", err)
	}
	remote := strings.TrimRight(strings.TrimSpace(config.Remote), "/")
	if err := validateRcloneRemote(remote); err != nil {
		return nil, err
	}
	return &RcloneArchiveBackend{
		binary:  resolved,
		remote:  remote,
		command: exec.CommandContext,
	}, nil
}

func (b *RcloneArchiveBackend) Name() string {
	return "google-drive-rclone"
}

func (b *RcloneArchiveBackend) Durable() bool {
	return true
}

func (b *RcloneArchiveBackend) Put(ctx context.Context, name, sourcePath string) (ArchiveObject, error) {
	name = path.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" || strings.HasPrefix(name, "-") {
		return ArchiveObject{}, errors.New("invalid rclone archive object name")
	}
	sha, size, err := fileSHA256(sourcePath)
	if err != nil {
		return ArchiveObject{}, err
	}
	objectName := b.remote + "/" + name
	command := b.command(ctx, b.binary,
		"copyto",
		"--immutable",
		"--check-first",
		"--no-update-modtime",
		sourcePath,
		objectName,
	)
	if err := runRcloneCommand(command); err != nil {
		return ArchiveObject{}, fmt.Errorf("upload immutable Session archive: %w", err)
	}
	return ArchiveObject{Backend: b.Name(), Name: objectName, SHA256: sha, Size: size}, nil
}

func (b *RcloneArchiveBackend) Verify(ctx context.Context, object ArchiveObject) error {
	if object.Backend != b.Name() {
		return errors.New("archive object backend does not match Google Drive backend")
	}
	if !strings.HasPrefix(object.Name, b.remote+"/") {
		return errors.New("archive object is outside configured rclone remote")
	}
	relative := strings.TrimPrefix(object.Name, b.remote+"/")
	if relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") {
		return errors.New("archive object has an unsafe rclone path")
	}
	command := b.command(ctx, b.binary, "cat", object.Name)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open rclone read-back stream: %w", err)
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start rclone read-back: %w", err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, stdout)
	waitErr := command.Wait()
	if copyErr != nil {
		return fmt.Errorf("read Google Drive archive: %w", copyErr)
	}
	if waitErr != nil {
		return formatRcloneError(waitErr, stderr.String())
	}
	actualSHA := hex.EncodeToString(hash.Sum(nil))
	if actualSHA != object.SHA256 || size != object.Size {
		return fmt.Errorf(
			"Google Drive archive read-back mismatch: sha=%s/%s size=%d/%d",
			actualSHA, object.SHA256, size, object.Size,
		)
	}
	return nil
}

func validateRcloneRemote(remote string) error {
	if remote == "" {
		return errors.New("rclone archive remote is required")
	}
	if strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, "\x00\r\n") {
		return errors.New("invalid rclone archive remote")
	}
	colon := strings.IndexByte(remote, ':')
	if colon <= 0 {
		return errors.New("rclone archive remote must include a named remote prefix, for example gdrive:folder")
	}
	return nil
}

func runRcloneCommand(command *exec.Cmd) error {
	var stderr limitedBuffer
	command.Stdout = io.Discard
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return formatRcloneError(err, stderr.String())
	}
	return nil
}

func formatRcloneError(err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, sanitizeTransportText(output))
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := rcloneErrorOutputLimit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}
