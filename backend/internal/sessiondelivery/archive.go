package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ArchiveObject struct {
	Backend string `json:"backend"`
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type ArchiveBackend interface {
	Name() string
	Durable() bool
	Put(context.Context, string, string) (ArchiveObject, error)
	Verify(context.Context, ArchiveObject) error
}

// LocalArchiveBackend is intentionally non-durable. It is suitable for local
// validation and staging, but Store.PurgeHour must never be enabled from a
// batch archived only to this backend.
type LocalArchiveBackend struct {
	dir string
}

func NewLocalArchiveBackend(dir string) (*LocalArchiveBackend, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("local archive directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create local archive directory: %w", err)
	}
	return &LocalArchiveBackend{dir: dir}, nil
}

func (b *LocalArchiveBackend) Name() string {
	return "local"
}

func (b *LocalArchiveBackend) Durable() bool {
	return false
}

func (b *LocalArchiveBackend) Put(ctx context.Context, name, sourcePath string) (ArchiveObject, error) {
	if err := ctx.Err(); err != nil {
		return ArchiveObject{}, err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ArchiveObject{}, errors.New("invalid archive object name")
	}
	sourceSHA, sourceSize, err := fileSHA256(sourcePath)
	if err != nil {
		return ArchiveObject{}, err
	}
	destination := filepath.Join(b.dir, name)
	if _, err := os.Stat(destination); err == nil {
		existingSHA, existingSize, hashErr := fileSHA256(destination)
		if hashErr != nil {
			return ArchiveObject{}, hashErr
		}
		if existingSHA != sourceSHA || existingSize != sourceSize {
			return ArchiveObject{}, fmt.Errorf("archive object %s already exists with different content", name)
		}
		return ArchiveObject{Backend: b.Name(), Name: destination, SHA256: existingSHA, Size: existingSize}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ArchiveObject{}, fmt.Errorf("check local archive object: %w", err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return ArchiveObject{}, fmt.Errorf("open archive source: %w", err)
	}
	defer source.Close()
	tmp, err := os.CreateTemp(b.dir, ".archive-*")
	if err != nil {
		return ArchiveObject{}, fmt.Errorf("create archive temp object: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return ArchiveObject{}, err
	}
	if _, err := copyContext(ctx, tmp, source); err != nil {
		_ = tmp.Close()
		return ArchiveObject{}, fmt.Errorf("copy archive object: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return ArchiveObject{}, fmt.Errorf("sync archive object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return ArchiveObject{}, fmt.Errorf("close archive object: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return ArchiveObject{}, fmt.Errorf("commit archive object: %w", err)
	}
	if err := syncDirectory(b.dir); err != nil {
		return ArchiveObject{}, fmt.Errorf("sync archive directory: %w", err)
	}
	return ArchiveObject{Backend: b.Name(), Name: destination, SHA256: sourceSHA, Size: sourceSize}, nil
}

func (b *LocalArchiveBackend) Verify(ctx context.Context, object ArchiveObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleaned := filepath.Clean(object.Name)
	relative, err := filepath.Rel(b.dir, cleaned)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("archive object is outside local archive directory")
	}
	actualSHA, actualSize, err := fileSHA256(cleaned)
	if err != nil {
		return err
	}
	if actualSHA != object.SHA256 || actualSize != object.Size {
		return fmt.Errorf("local archive read-back mismatch: sha=%s/%s size=%d/%d", actualSHA, object.SHA256, actualSize, object.Size)
	}
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 256<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
