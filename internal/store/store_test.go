package store

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ReadDocument / WriteDocument ---

func TestWriteAndReadDocumentWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	doc := &Document{
		Frontmatter: map[string]any{
			"title":  "Hello",
			"status": "draft",
			"count":  42,
		},
		Body: "# Hello World\n\nThis is the body.\n",
	}

	err := WriteDocument(path, doc)
	require.NoError(t, err)

	got, err := ReadDocument(path)
	require.NoError(t, err)

	assert.Equal(t, "Hello", GetString(got.Frontmatter, "title"))
	assert.Equal(t, "draft", GetString(got.Frontmatter, "status"))
	assert.Contains(t, got.Body, "# Hello World")
	assert.Contains(t, got.Body, "This is the body.")
}

func TestWriteAndReadDocumentWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.md")

	doc := &Document{
		Frontmatter: map[string]any{},
		Body:        "Just a plain markdown file.\n",
	}

	err := WriteDocument(path, doc)
	require.NoError(t, err)

	got, err := ReadDocument(path)
	require.NoError(t, err)

	assert.Empty(t, got.Frontmatter)
	assert.Equal(t, "Just a plain markdown file.\n", got.Body)
}

func TestReadDocumentNonExistent(t *testing.T) {
	_, err := ReadDocument("/nonexistent/path/file.md")
	assert.Error(t, err)
}

func TestWriteDocumentCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "test.md")

	doc := &Document{
		Frontmatter: map[string]any{"key": "value"},
		Body:        "body",
	}

	err := WriteDocument(path, doc)
	require.NoError(t, err)

	got, err := ReadDocument(path)
	require.NoError(t, err)
	assert.Equal(t, "value", GetString(got.Frontmatter, "key"))
}

// --- ReadBody / WriteBody ---

func TestWriteAndReadBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")

	err := WriteBody(path, "# Just Body\n\nNo frontmatter here.\n")
	require.NoError(t, err)

	body, err := ReadBody(path)
	require.NoError(t, err)
	assert.Equal(t, "# Just Body\n\nNo frontmatter here.\n", body)
}

func TestReadBodyIgnoresFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fm.md")

	content := "---\ntitle: Ignored\n---\n\nBody content only.\n"
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	body, err := ReadBody(path)
	require.NoError(t, err)
	assert.Contains(t, body, "Body content only.")
	assert.NotContains(t, body, "title: Ignored")
}

func TestWriteBodyCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c.md")

	err := WriteBody(path, "nested")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "nested", string(data))
}

// --- Exists ---

func TestExistsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.md")
	require.NoError(t, os.WriteFile(path, []byte("hi"), 0644))

	assert.True(t, Exists(path))
}

func TestExistsFalse(t *testing.T) {
	assert.False(t, Exists("/nonexistent/path/does/not/exist.md"))
}

// --- WithLock ---

func TestWithLockBasicOperation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locktest")

	called := false
	err := WithLock(path, DefaultLockTimeout, func() error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestWithLockConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent")

	var counter int64
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithLock(path, 10*time.Second, func() error {
				// Read-modify-write under lock
				val := atomic.LoadInt64(&counter)
				time.Sleep(time.Millisecond) // simulate work
				atomic.StoreInt64(&counter, val+1)
				return nil
			})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(10), atomic.LoadInt64(&counter))
}

func TestWithReadLockBasicOperation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readlocktest")

	called := false
	err := WithReadLock(path, DefaultLockTimeout, func() error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestWithLockTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timeouttest")

	// Acquire lock in a goroutine and hold it
	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = WithLock(path, 10*time.Second, func() error {
			close(locked) // signal lock acquired
			<-release     // hold lock until told to release
			return nil
		})
	}()

	<-locked // wait for lock to be held

	// Try to acquire with a very short timeout — should fail
	err := WithLock(path, 200*time.Millisecond, func() error {
		t.Fatal("callback should not have been called")
		return nil
	})
	assert.Error(t, err, "expected timeout error when lock is held")

	close(release) // let the first goroutine release the lock
}

// --- Frontmatter helpers ---

func TestGetString(t *testing.T) {
	fm := map[string]any{"name": "otto", "count": 42}
	assert.Equal(t, "otto", GetString(fm, "name"))
	assert.Equal(t, "", GetString(fm, "missing"))
	assert.Equal(t, "", GetString(fm, "count")) // wrong type
}

func TestGetInt(t *testing.T) {
	fm := map[string]any{
		"int_val":   42,
		"float_val": float64(99),
		"int64_val": int64(7),
		"str_val":   "not a number",
	}
	assert.Equal(t, 42, GetInt(fm, "int_val"))
	assert.Equal(t, 99, GetInt(fm, "float_val"))
	assert.Equal(t, 7, GetInt(fm, "int64_val"))
	assert.Equal(t, 0, GetInt(fm, "str_val"))
	assert.Equal(t, 0, GetInt(fm, "missing"))
}

func TestGetBool(t *testing.T) {
	fm := map[string]any{"flag": true, "off": false, "str": "true"}
	assert.True(t, GetBool(fm, "flag"))
	assert.False(t, GetBool(fm, "off"))
	assert.False(t, GetBool(fm, "str")) // wrong type
	assert.False(t, GetBool(fm, "missing"))
}

func TestGetStringSlice(t *testing.T) {
	fm := map[string]any{
		"tags":  []any{"go", "cli", "tool"},
		"empty": []any{},
		"mixed": []any{"a", 1, "b"},
		"str":   "not a slice",
	}
	assert.Equal(t, []string{"go", "cli", "tool"}, GetStringSlice(fm, "tags"))
	assert.Equal(t, []string{}, GetStringSlice(fm, "empty"))
	assert.Equal(t, []string{"a", "b"}, GetStringSlice(fm, "mixed")) // skips non-strings
	assert.Nil(t, GetStringSlice(fm, "str"))
	assert.Nil(t, GetStringSlice(fm, "missing"))
}

func TestGetTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	fm := map[string]any{
		"time_val":   now,
		"string_val": now.Format(time.RFC3339),
		"bad_string": "not-a-time",
		"int_val":    42,
	}
	assert.Equal(t, now, GetTime(fm, "time_val"))
	assert.Equal(t, now.UTC(), GetTime(fm, "string_val").UTC())
	assert.True(t, GetTime(fm, "bad_string").IsZero())
	assert.True(t, GetTime(fm, "int_val").IsZero())
	assert.True(t, GetTime(fm, "missing").IsZero())
}

func TestSetField(t *testing.T) {
	// Works with nil map
	fm := SetField(nil, "key", "value")
	assert.Equal(t, "value", fm["key"])

	// Works with existing map
	fm = SetField(fm, "another", 42)
	assert.Equal(t, "value", fm["key"])
	assert.Equal(t, 42, fm["another"])

	// Overwrites existing key
	fm = SetField(fm, "key", "updated")
	assert.Equal(t, "updated", fm["key"])
}

func TestFormatTimeAndNow(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	assert.Equal(t, "2025-06-15T10:30:00Z", FormatTime(ts))

	nowStr := Now()
	_, err := time.Parse(time.RFC3339, nowStr)
	assert.NoError(t, err)
}
