package account

import (
	"os"
	"path/filepath"
	"strings"
)

const defaultUploadDirectory = ".run/uploads"

// UploadDirectory is shared by the upload handler and static file route.
func UploadDirectory() string {
	if directory := strings.TrimSpace(os.Getenv("UPLOAD_DIR")); directory != "" {
		return directory
	}
	return filepath.Clean(defaultUploadDirectory)
}
