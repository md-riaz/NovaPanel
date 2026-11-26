package fileop

import (
	"encoding/json"
	"errors"
	"io"
	"os"
)

type ArchiveRequest struct {
	Base, Name, Operation string
	Paths                 []string
	Archive, Dest         string
}

func ArchiveHelper(in io.Reader, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req ArchiveRequest
	if err := json.NewDecoder(io.LimitReader(in, 65536)).Decode(&req); err != nil {
		return err
	}
	root, err := OpenDocroot(req.Base, req.Name)
	if err == nil {
		root.Close()
		err = errors.New("unsupported operation")
	}
	return json.NewEncoder(out).Encode(Response{Code: errorCode(err)})
}
