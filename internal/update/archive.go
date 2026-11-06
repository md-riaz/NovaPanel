package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"io"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
)

// stage authenticates the complete compressed artifact before reading any tar header.
// root is a private agent-owned installation directory, never caller-controlled.
func stage(ctx context.Context, root string, a Artifact, src io.Reader) (dir string, err error) {
	file, err := os.CreateTemp(root, ".artifact-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = verifyArtifact(ctx, a, src, file); err != nil {
		return "", err
	}
	if _, err = file.Seek(0, 0); err != nil {
		return "", err
	}
	dir, err = os.MkdirTemp(root, ".update-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
			dir = ""
		}
	}()
	reader := bufio.NewReader(file)
	var input io.Reader = reader
	magic, _ := reader.Peek(2)
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		z, e := gzip.NewReader(reader)
		if e != nil {
			return dir, invalidArchive()
		}
		defer z.Close()
		input = z
	}
	bounded := &io.LimitedReader{R: contextReader{ctx, input}, N: maxArtifactBytes + 1}
	tr := tar.NewReader(bounded)
	seen := map[string]bool{}
	var total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return dir, invalidArchive()
		}
		if ctx.Err() != nil {
			return dir, ctx.Err()
		}
		if h.Typeflag != tar.TypeReg || h.Linkname != "" || len(h.PAXRecords) > 0 || h.Size <= 0 || h.Size > 128<<20 || seen[h.Name] {
			return dir, invalidArchive()
		}
		switch h.Name {
		case "panel-agent", "panel-web", "novapanelctl":
		default:
			return dir, invalidArchive()
		}
		total += h.Size
		if total > 384<<20 {
			return dir, invalidArchive()
		}
		seen[h.Name] = true
		f, e := os.OpenFile(filepath.Join(dir, h.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if e != nil {
			return dir, e
		}
		_, e = io.Copy(f, tr)
		if e == nil {
			e = f.Chmod(0755)
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return dir, e
		}
		if closeErr != nil {
			return dir, closeErr
		}
	}
	// Drain to validate gzip checksum and refuse hidden non-padding bytes after tar EOF.
	tail := make([]byte, 32768)
	for {
		n, e := bounded.Read(tail)
		for _, b := range tail[:n] {
			if b != 0 {
				return dir, invalidArchive()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return dir, invalidArchive()
		}
	}
	if len(seen) != 3 || bounded.N <= 0 {
		return dir, invalidArchive()
	}
	return dir, nil
}
func invalidArchive() error { return rpcerr.Errf("invalid_argument", "invalid update archive") }
