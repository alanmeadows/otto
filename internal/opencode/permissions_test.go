package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPermissions(t *testing.T) {
	perms := DefaultPermissions()

	assert.NotEmpty(t, perms.Permission)
	assert.Equal(t, "allow", perms.Permission["edit"])
	assert.Equal(t, "allow", perms.Permission["bash"])
	assert.Equal(t, "allow", perms.Permission["read"])
	assert.Equal(t, "allow", perms.Permission["doom_loop"])
	assert.Equal(t, "allow", perms.Permission["external_directory"])

	// All values should be "allow"
	for key, val := range perms.Permission {
		assert.Equal(t, "allow", val, "permission %s should be allow", key)
	}

	// Should have 16 permissions
	assert.Len(t, perms.Permission, 16)
}

func TestEnsurePermissions(t *testing.T) {
	dir := t.TempDir()

	err := EnsurePermissions(dir)
	require.NoError(t, err)

	// Real file lives in .otto/
	realPath := filepath.Join(dir, ".otto", "opencode.json")
	assert.FileExists(t, realPath)

	data, err := os.ReadFile(realPath)
	require.NoError(t, err)

	var cfg PermissionConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "allow", cfg.Permission["edit"])
	assert.Equal(t, "allow", cfg.Permission["bash"])
	assert.Len(t, cfg.Permission, 16)

	// Root-level path should be a symlink to .otto/opencode.json.
	linkPath := filepath.Join(dir, "opencode.json")
	target, err := os.Readlink(linkPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(".otto", "opencode.json"), target)

	// Should be readable through the symlink.
	linkData, err := os.ReadFile(linkPath)
	require.NoError(t, err)
	assert.Equal(t, data, linkData)
}

func TestEnsurePermissionsCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")

	err := EnsurePermissions(dir)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, ".otto", "opencode.json"))
	assert.FileExists(t, filepath.Join(dir, "opencode.json"))
}

func TestEnsurePermissionsIdempotent(t *testing.T) {
	dir := t.TempDir()

	// Write twice — should not error
	require.NoError(t, EnsurePermissions(dir))
	require.NoError(t, EnsurePermissions(dir))

	data, err := os.ReadFile(filepath.Join(dir, ".otto", "opencode.json"))
	require.NoError(t, err)

	var cfg PermissionConfig
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Len(t, cfg.Permission, 16)
}

func TestEnsurePermissionsJSONFormat(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, EnsurePermissions(dir))

	data, err := os.ReadFile(filepath.Join(dir, ".otto", "opencode.json"))
	require.NoError(t, err)

	// Should be valid, pretty-printed JSON
	assert.True(t, json.Valid(data))
	assert.Contains(t, string(data), "  ") // indented
}
