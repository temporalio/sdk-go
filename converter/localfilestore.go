package converter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

// LocalFileStoreDriver is a StorageDriver that persists payloads as files on
// the local filesystem. It is intended for local development and testing only,
// not for production use.
//
// Each payload is serialized with proto.Marshal, written to a file named by a
// content-addressable hash, and the file path is recorded in the returned
// StorageDriverClaim.
//
// NOTE: Experimental
type LocalFileStoreDriver struct {
	// Dir is the directory where payload files are stored. It will be created
	// if it does not exist.
	Dir string
}

var _ StorageDriver = (*LocalFileStoreDriver)(nil)

// NewLocalFileStoreDriver creates a LocalFileStoreDriver that stores payloads
// under dir.
func NewLocalFileStoreDriver(dir string) *LocalFileStoreDriver {
	return &LocalFileStoreDriver{Dir: dir}
}

func (d *LocalFileStoreDriver) Name() string { return "local-file-store" }

func (d *LocalFileStoreDriver) Type() string { return "local-file-store" }

func (d *LocalFileStoreDriver) Store(_ StorageDriverStoreContext, payloads []*commonpb.Payload) ([]StorageDriverClaim, error) {
	absDir, err := filepath.Abs(d.Dir)
	if err != nil {
		return nil, fmt.Errorf("local file store: resolve dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("local file store: create dir: %w", err)
	}

	claims := make([]StorageDriverClaim, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("local file store: marshal payload %d: %w", i, err)
		}

		hash := sha256.Sum256(data)
		name := hex.EncodeToString(hash[:])
		path := filepath.Join(absDir, name)

		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("local file store: write %s: %w", path, err)
		}

		claims[i] = StorageDriverClaim{
			ClaimData: map[string]string{
				"path": path,
			},
		}
	}
	return claims, nil
}

func (d *LocalFileStoreDriver) Retrieve(_ StorageDriverRetrieveContext, claims []StorageDriverClaim) ([]*commonpb.Payload, error) {
	payloads := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		path, ok := c.ClaimData["path"]
		if !ok {
			return nil, fmt.Errorf("local file store: claim %d missing path", i)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("local file store: read %s: %w", path, err)
		}

		var p commonpb.Payload
		if err := proto.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("local file store: unmarshal payload %d: %w", i, err)
		}
		payloads[i] = &p
	}
	return payloads, nil
}
