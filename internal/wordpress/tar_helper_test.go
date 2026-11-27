package wordpress

import (
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTransientDownloadClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"connection refused", &url.Error{Op: "Get", URL: "https://wordpress.org/", Err: unix.ECONNREFUSED}, true},
		{"DNS", &net.DNSError{Err: "temporary", Name: "wordpress.org", IsTemporary: true}, true},
		{"truncated transfer", io.ErrUnexpectedEOF, true},
		{"certificate", &url.Error{Op: "Get", URL: "https://wordpress.org/", Err: x509.UnknownAuthorityError{}}, false},
		{"refused redirect", &url.Error{Op: "Get", URL: "https://wordpress.org/", Err: errStaged}, false},
		{"verification mismatch", errors.New("checksum mismatch"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transientDownload(tc.err); got != tc.retry {
				t.Fatalf("transient=%v", got)
			}
		})
	}
}
