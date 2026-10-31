package wordpress

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

// StageBinding is trusted queue identity, not RPC input. It is persisted before
// extraction. A stage never grants permission to publish or execute its files.
type StageBinding struct {
	JobID, SiteID, Docroot string
	UID, GID               uint32
}

var stageToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ExtractStaged prepares a private root-owned tree from canonical verified bytes.
// Retrying the original binding validates exact existing bytes without overwrite.
// No PHP, site-writable docroot mutation or operation checkpoint occurs here.
func ExtractStaged(ctx context.Context, p StagedPaths, r runner.Runner, root string, b StageBinding) error {
	if os.Geteuid() != 0 {
		return errStaged
	}
	return extractStaged(ctx, p, r, root, b, ApprovedArtifacts(), 0, false, (*os.File).Sync)
}

func extractStaged(ctx context.Context, p StagedPaths, r runner.Runner, root string, b StageBinding, m Manifest, owner uint32, fixture bool, syncFile func(*os.File) error) error {
	if !stageToken.MatchString(b.JobID) || !stageToken.MatchString(b.SiteID) || !filepath.IsAbs(b.Docroot) || filepath.Clean(b.Docroot) != b.Docroot || b.UID == 0 || b.GID == 0 {
		return errStaged
	}
	// Custody must be separate from the whole site home, not merely public/.
	home := filepath.Dir(b.Docroot)
	if root == home || strings.HasPrefix(root, home+"/") || home == root || strings.HasPrefix(home, root+"/") {
		return errStaged
	}
	_, err := consumeStaged(ctx, p, r, m, owner, fixture, func(ctx context.Context, archive *os.File, e ArtifactEvidence) error {
		base, err := openStaged(root, true, owner, fixture)
		if err != nil {
			return err
		}
		defer base.Close()
		original, err := base.Stat()
		if err != nil {
			return err
		}
		stage, err := stageDirectory(base, b.JobID, owner)
		if err != nil {
			return err
		}
		defer stage.Close()
		record, _ := json.Marshal(struct {
			Binding  StageBinding
			Evidence ArtifactEvidence
		}{b, e})
		if err = stageFile(ctx, stage, "identity.json", bytes.NewReader(record), int64(len(record)), owner, syncFile); err != nil {
			return err
		}
		if err = syncFile(stage); err != nil {
			return err
		}
		if err = syncFile(base); err != nil {
			return err
		}
		// No entry is accepted merely because the original operation owns the parent.
		entries, err := stage.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != "identity.json" && entry.Name() != "tree" {
				return errStaged
			}
		}
		tree, err := stageDirectory(stage, "tree", owner)
		if err != nil {
			return err
		}
		defer tree.Close()
		st, err := archive.Stat()
		if err != nil {
			return err
		}
		z, err := zip.NewReader(archive, st.Size())
		if err != nil {
			return err
		}
		files := map[string]*zip.File{}
		dirs := map[string]bool{"": true}
		for _, f := range z.File {
			name := strings.TrimSuffix(strings.TrimPrefix(f.Name, "wordpress/"), "/")
			if name == "" {
				continue
			}
			if f.Mode().IsDir() {
				dirs[name] = true
			} else {
				files[name] = f
			}
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				dirs[parent] = true
			}
		}
		if err = validateStageTree(ctx, tree, "", files, dirs, owner, syncFile); err != nil {
			return err
		}
		for name, f := range files {
			if err = ctx.Err(); err != nil {
				return err
			}
			current, err := stageDirectory(tree, ".", owner)
			if err != nil {
				return err
			}
			parts := strings.Split(name, "/")
			for _, part := range parts[:len(parts)-1] {
				next, e := stageDirectory(current, part, owner)
				if e == nil {
					e = syncFile(current)
				}
				current.Close()
				if e != nil {
					if next != nil {
						next.Close()
					}
					return e
				}
				current = next
			}
			input, err := f.Open()
			if err != nil {
				current.Close()
				return err
			}
			err = stageFile(ctx, current, parts[len(parts)-1], input, int64(f.UncompressedSize64), owner, syncFile)
			input.Close()
			if err == nil {
				err = syncFile(current)
			}
			current.Close()
			if err != nil {
				return err
			}
		}
		// Explicit empty directories in the pinned archive are part of inventory too.
		for name := range dirs {
			current, err := stageDirectory(tree, ".", owner)
			if err != nil {
				return err
			}
			if name != "" {
				for _, part := range strings.Split(name, "/") {
					next, e := stageDirectory(current, part, owner)
					if e == nil {
						e = syncFile(current)
					}
					current.Close()
					if e != nil {
						if next != nil {
							next.Close()
						}
						return e
					}
					current = next
				}
			}
			err = syncFile(current)
			current.Close()
			if err != nil {
				return err
			}
		}
		if err = validateStageTree(ctx, tree, "", files, dirs, owner, syncFile); err != nil {
			return err
		}
		if err = syncFile(tree); err != nil {
			return err
		}
		if err = syncFile(stage); err != nil {
			return err
		}
		if err = syncFile(base); err != nil {
			return err
		}
		reopened, err := openStaged(root, true, owner, fixture)
		if err != nil {
			return err
		}
		defer reopened.Close()
		now, err := reopened.Stat()
		if err != nil || !os.SameFile(original, now) {
			return errStaged
		}
		// Verify the operation entry still names our held inode.
		check, err := stageDirectory(base, b.JobID, owner)
		if err != nil {
			return err
		}
		defer check.Close()
		a, err := stage.Stat()
		if err != nil {
			return err
		}
		c, err := check.Stat()
		if err != nil || !os.SameFile(a, c) {
			return errStaged
		}
		checkTree, err := stageDirectory(stage, "tree", owner)
		if err != nil {
			return err
		}
		defer checkTree.Close()
		a, err = tree.Stat()
		if err != nil {
			return err
		}
		c, err = checkTree.Stat()
		if err != nil || !os.SameFile(a, c) {
			return errStaged
		}
		return ctx.Err()
	})
	return err
}

func stageDirectory(parent *os.File, name string, owner uint32) (*os.File, error) {
	if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && err != unix.EEXIST {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != owner || st.Mode&07777 != 0700 {
		f.Close()
		return nil, errStaged
	}
	return f, nil
}

func stageFile(ctx context.Context, parent *os.File, name string, input io.Reader, size int64, owner uint32, syncFile func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0400)
	if err == unix.EEXIST {
		fd, err = unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != owner || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0444 || st.Nlink != 1 || st.Size != size {
			return errStaged
		}
		// Compare streamed bytes, not just size or an identity receipt.
		a := make([]byte, 32768)
		b := make([]byte, 32768)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, e := io.ReadFull(input, a)
			k, x := io.ReadFull(f, b)
			if n != k || !bytes.Equal(a[:n], b[:k]) {
				return errStaged
			}
			if e != nil {
				if (e == io.EOF || e == io.ErrUnexpectedEOF) && e == x {
					break
				}
				return errStaged
			}
			if x != nil {
				return errStaged
			}
		}
		return syncFile(f)
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(input, size+1))
	if err != nil || n != size {
		return errStaged
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = f.Chmod(0444); err != nil {
		return err
	}
	return syncFile(f)
}

func validateStageTree(ctx context.Context, dir *os.File, prefix string, files map[string]*zip.File, dirs map[string]bool, owner uint32, syncFile func(*os.File) error) error {
	// Use a new open file description so repeated scans do not share offsets.
	scan, err := stageDirectory(dir, ".", owner)
	if err != nil {
		return err
	}
	defer scan.Close()
	entries, err := scan.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		if dirs[rel] {
			child, err := stageDirectory(dir, name, owner)
			if err != nil {
				return err
			}
			err = validateStageTree(ctx, child, rel, files, dirs, owner, syncFile)
			child.Close()
			if err != nil {
				return err
			}
			continue
		}
		f := files[rel]
		if f == nil {
			return errStaged
		}
		input, err := f.Open()
		if err != nil {
			return err
		}
		err = stageFile(ctx, dir, name, input, int64(f.UncompressedSize64), owner, syncFile)
		input.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
