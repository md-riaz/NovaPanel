package dns

import (
	"context"
	"errors"
	"sync"
)

// Fake is an in-memory provider for tests.
type Fake struct {
	mu       sync.Mutex
	Records  map[string][]Record
	Verified map[string]bool
	TXT      map[string]map[string]bool
	Purges   map[string][][]string
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
func (f *Fake) Verify(_ context.Context, domain string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Verified[domain], nil
}
func (f *Fake) Purge(_ context.Context, zone string, urls []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Purges == nil {
		f.Purges = make(map[string][][]string)
	}
	f.Purges[zone] = append(f.Purges[zone], append([]string(nil), urls...))
	return nil
}
func (f *Fake) PresentTXT(_ context.Context, fqdn, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.TXT == nil {
		f.TXT = make(map[string]map[string]bool)
	}
	if f.TXT[fqdn] == nil {
		f.TXT[fqdn] = make(map[string]bool)
	}
	f.TXT[fqdn][value] = true
	return nil
}
func (f *Fake) CleanupTXT(_ context.Context, fqdn, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.TXT[fqdn], value)
	if len(f.TXT[fqdn]) == 0 {
		delete(f.TXT, fqdn)
	}
	return nil
}
