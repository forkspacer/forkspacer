package file

import (
	"os"
	"sync"
)

const (
	BaseDir = "/internal-data"
	GitPath = "/usr/bin/git"
)

var (
	ensureBaseDirOnce sync.Once
	baseDirPath       string
)

// GetBaseDir returns the base directory for temporary files.
// It ensures the directory exists or falls back to /tmp for dev mode.
func GetBaseDir() string {
	ensureBaseDirOnce.Do(func() {
		// Try to use the BaseDir constant
		if err := os.MkdirAll(BaseDir, 0755); err != nil {
			// If we can't create /internal-data (dev mode), use /tmp
			baseDirPath = "/tmp"
		} else {
			baseDirPath = BaseDir
		}
	})
	return baseDirPath
}
