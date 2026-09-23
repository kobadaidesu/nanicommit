package storage_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/storage"
)

const oid = "0123456789abcdef0123456789abcdef01234567"

func TestSaveEventPermissionsAndReplace(t *testing.T) {
	common := t.TempDir()
	if n, err := storage.CountEvents(common); err != nil || n != 0 {
		t.Fatalf("CountEvents before = %d, %v", n, err)
	}
	path, err := storage.SaveEvent(common, oid, []byte(`{"v":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(common, "commitcoach", "events", oid+".json"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if runtime.GOOS != "windows" {
		for p, want := range map[string]os.FileMode{
			filepath.Join(common, "commitcoach"):           0o700,
			filepath.Join(common, "commitcoach", "events"): 0o700,
			path: 0o600,
		} {
			fi, err := os.Stat(p)
			if err != nil || fi.Mode().Perm() != want {
				t.Errorf("%s: mode %v, %v; want %v", p, fi.Mode().Perm(), err, want)
			}
		}
	}
	if _, err := storage.SaveEvent(common, oid, []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"v":2}` {
		t.Errorf("content after replace = %s", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if n, err := storage.CountEvents(common); err != nil || n != 1 {
		t.Errorf("CountEvents = %d, %v", n, err)
	}
}

func TestSaveEventRejectsBadNames(t *testing.T) {
	for _, bad := range []string{"", "../../etc/passwd", "HEAD", "ABCDEF", "abc/def"} {
		if _, err := storage.SaveEvent(t.TempDir(), bad, []byte("{}")); err == nil {
			t.Errorf("SaveEvent accepted %q", bad)
		}
	}
}

func TestConcurrentWritesNeverMix(t *testing.T) {
	common := t.TempDir()
	payload := func(i int) []byte {
		b, _ := json.Marshal(map[string]string{"writer": fmt.Sprint(i), "pad": strings.Repeat(fmt.Sprint(i%10), 200_000)})
		return b
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := storage.SaveEvent(common, oid, payload(i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	path, _ := storage.EventPath(common, oid)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	var i int
	fmt.Sscan(got["writer"], &i)
	if string(data) != string(payload(i)) {
		t.Error("result mixes the output of several writers")
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temporary files left behind: %d entries", len(entries))
	}
}

func TestSaveFailureLeavesNoFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	common := t.TempDir()
	dir := storage.EventsDir(common)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := storage.SaveEvent(common, oid, []byte("{}")); err == nil {
		t.Fatal("SaveEvent succeeded in a read-only directory")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left behind: %v", entries)
	}
}

func TestWriteFileAtomicMissingDirectory(t *testing.T) {
	if err := storage.WriteFileAtomic(filepath.Join(t.TempDir(), "no", "such", "x.json"), []byte("{}")); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}
