package store

import (
	"context"
	"errors"
	"testing"
)

func TestClosedStoreAdmissionPrecedesClaimValidation(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		call func(*Store) error
		want string
	}{
		{
			name: "mismatched job claim",
			call: func(s *Store) error {
				return s.CreateJobWithClaim(ctx, Job{ID: "j"}, JobClaim{JobID: "other"})
			},
			want: "claim job mismatch",
		},
		{
			name: "empty sealed claim",
			call: func(s *Store) error { return s.PublishClaim(ctx, "j", nil, nil) },
			want: "empty sealed claim",
		},
		{
			name: "empty nonce",
			call: func(s *Store) error { return s.PublishClaim(ctx, "j", nil, []byte("c")) },
			want: "empty sealed claim",
		},
		{
			name: "empty ciphertext",
			call: func(s *Store) error { return s.PublishClaim(ctx, "j", []byte("n"), nil) },
			want: "empty sealed claim",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := fresh(t)
			if err := tt.call(s); err == nil || err.Error() != tt.want {
				t.Fatalf("open store returned %v, want %q", err, tt.want)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := tt.call(s); !errors.Is(err, ErrStoreClosed) {
				t.Fatalf("closed store returned %v, want ErrStoreClosed", err)
			}
		})
	}
}
