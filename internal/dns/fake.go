package dns

import (
	"context"
	"errors"
	"sync"
)

// Fake is an in-memory provider for tests.
type Fake struct {
	mu      sync.Mutex
	Records map[string][]Record
}

func (f *Fake) List(_ context.Context, zone string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Record(nil), f.Records[zone]...), nil
}
func (f *Fake) Create(_ context.Context, zone string, record Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Records == nil {
		f.Records = make(map[string][]Record)
	}
	for _, r := range f.Records[zone] {
		if r.ID == record.ID {
			return errors.New("record already exists")
		}
	}
	f.Records[zone] = append(f.Records[zone], record)
	return nil
}
func (f *Fake) Update(_ context.Context, zone string, record Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.Records[zone] {
		if r.ID == record.ID {
			f.Records[zone][i] = record
			return nil
		}
	}
	return errors.New("record not found")
}
func (f *Fake) Delete(_ context.Context, zone string, record Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.Records[zone] {
		if r.ID == record.ID {
			f.Records[zone] = append(f.Records[zone][:i], f.Records[zone][i+1:]...)
			return nil
		}
	}
	return nil
}
func (*Fake) Verify(context.Context, string) (bool, error)     { return false, nil }
func (*Fake) Purge(context.Context, string, []string) error    { return nil }
func (*Fake) PresentTXT(context.Context, string, string) error { return nil }
func (*Fake) CleanupTXT(context.Context, string, string) error { return nil }
