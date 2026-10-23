package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type memObj struct {
	body []byte
	sum  []byte
	at   time.Time
}

type memStore struct {
	mu              sync.Mutex
	objs            map[string]memObj
	order           []string
	headMismatch    bool
	shaMismatch     bool
	failHead        bool
	failPutContains string
	failPutLeft     int
	putAttempts     int
	failDelete      map[string]bool
	deletes         int
	now             func() time.Time
}

func newMemStore(now func() time.Time) *memStore {
	if now == nil {
		now = time.Now
	}
	return &memStore{objs: map[string]memObj{}, failDelete: map[string]bool{}, now: now}
}

func (m *memStore) Put(_ context.Context, up Upload) error {
	body, err := io.ReadAll(up.Body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if up.SHA256 != nil && !bytes.Equal(up.SHA256, sum[:]) {
		return errClass(ExitUpload, "upload", "checksum mismatch")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putAttempts++
	if m.failPutLeft > 0 {
		m.failPutLeft--
		return errClass(ExitUpload, "upload", "could not upload the backup")
	}
	if m.failPutContains != "" && strings.Contains(up.Key, m.failPutContains) {
		return errClass(ExitUpload, "upload", "could not upload the backup")
	}
	m.objs[up.Key] = memObj{body: body, sum: sum[:], at: m.now()}
	m.order = append(m.order, up.Key)
	return nil
}

func (m *memStore) Head(_ context.Context, key string) (ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objs[key]
	if !ok {
		return ObjectInfo{}, errClass(ExitUpload, "upload", "the uploaded backup is missing")
	}
	if m.failHead {
		return ObjectInfo{}, errClass(ExitUpload, "upload", "could not verify the uploaded backup")
	}
	info := ObjectInfo{Key: key, Size: int64(len(obj.body)), LastModified: obj.at, SHA256: append([]byte(nil), obj.sum...)}
	if m.headMismatch {
		info.Size++
	}
	if m.shaMismatch && len(info.SHA256) > 0 {
		info.SHA256[0] ^= 0xff
	}
	return info, nil
}

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objs[key]
	if !ok {
		return nil, errClass(ExitUpload, "upload", "the backup is missing")
	}
	return append([]byte(nil), obj.body...), nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ObjectInfo
	for key, obj := range m.objs {
		if strings.HasPrefix(key, prefix) {
			out = append(out, ObjectInfo{Key: key, Size: int64(len(obj.body)), LastModified: obj.at, SHA256: obj.sum})
		}
	}
	return out, nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes++
	if m.failDelete[key] {
		return errClass(ExitSweep, "sweep", "could not delete an expired backup")
	}
	delete(m.objs, key)
	return nil
}

func (m *memStore) ServerNow(context.Context) (time.Time, error) {
	if m.now == nil {
		return time.Time{}, errClass(ExitSweep, "clock", "the backup server time is not known")
	}
	server := m.now()
	if server.IsZero() {
		return time.Time{}, errClass(ExitSweep, "clock", "the backup server time is not known")
	}
	return server, nil
}

func (m *memStore) putAt(key string, body []byte, at time.Time) {
	sum := sha256.Sum256(body)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = memObj{body: append([]byte(nil), body...), sum: sum[:], at: at}
}

func (m *memStore) putCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.order)
}

func (m *memStore) deleteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deletes
}

func (m *memStore) bodies() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][]byte
	for _, key := range m.order {
		out = append(out, m.objs[key].body)
	}
	return out
}

func (m *memStore) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for key := range m.objs {
		out = append(out, key)
	}
	return out
}

func (m *memStore) get(key string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objs[key].body
}

func (m *memStore) bodyForSuffix(suffix string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, obj := range m.objs {
		if strings.HasSuffix(key, suffix) {
			return obj.body
		}
	}
	return nil
}

func captureLog(b *strings.Builder) func() {
	log.SetOutput(b)
	return func() { log.SetOutput(os.Stderr) }
}

type recordPinger struct {
	failed bool
	body   string
}

func (p *recordPinger) Ping(_ context.Context, _, body string, failed bool) error {
	p.failed = failed
	p.body = body
	return nil
}
