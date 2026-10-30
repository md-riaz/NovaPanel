package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk held directories so neither creation nor checks follow a replaced symlink.
func prepareTLSRoot(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("invalid TLS root %q", path)
	}
	parent, err := os.Open("/")
	if err != nil {
		return err
	}
	defer func() { parent.Close() }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		leaf := i == len(parts)-1
		mode := uint32(0755) // Shared config ancestors must remain traversable by panel-web.
		if leaf {
			mode = 0700
		}
		mkdirErr := unix.Mkdirat(int(parent.Fd()), part, mode)
		if mkdirErr != nil && mkdirErr != unix.EEXIST {
			return mkdirErr
		}
		fd, err := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("unsafe TLS directory: %w", err)
		}
		child := os.NewFile(uintptr(fd), part)
		parent.Close()
		parent = child
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return err
		}
		if mkdirErr == nil {
			// Set exact modes despite the service umask, only on directories we created.
			if st.Uid != uint32(os.Geteuid()) {
				return fmt.Errorf("new TLS directory has unexpected owner: %s", part)
			}
			if err := unix.Fchmod(fd, mode); err != nil {
				return err
			}
			if err := unix.Fstat(fd, &st); err != nil {
				return err
			}
		}
		if leaf {
			if st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
				return fmt.Errorf("TLS root must be agent-owned and private: %s", path)
			}
		} else if (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0022 != 0 {
			// A root-owned sticky scratch parent cannot replace an agent-owned child.
			if st.Uid != 0 || st.Mode&unix.S_ISVTX == 0 {
				return fmt.Errorf("unsafe TLS ancestor: %s", part)
			}
		}
	}
	return nil
}
